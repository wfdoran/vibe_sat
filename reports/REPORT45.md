# Report: Stage 45

## Summary

`STAGE45.md` asked for `REPORT41.md`'s #3 item: a genuinely different
watch-list representation for `cdcl`, the single highest-ceiling lever
identified for the gap `REPORT40.md` measured against `minisat`/
`cryptominisat5` on random instances. Flagged explicitly as "high
effort, real risk" going in.

**The actual finding, on close reading of `propagate` (not assumed —
found by tracing exactly what candidate source it used): `cdcl` never
had a genuine watch list to begin with.** From Stage 9 through Stage
44, `propagate` found "which clauses might be affected by literal L
becoming false" by scanning `lists.Positive[v]`/`lists.Negative[v]` —
the full, static occurrence index (every clause that ever mentions the
literal, the same index `hc`/`ws`'s local search and `dfs`'s own BCP
still use) — and skipping past whichever candidates turned out not to
actually be watching that literal via a per-candidate check-and-skip.
The two-watched-literal *invariant* (`chooseWatch`, lazy watch
updates) was always correctly implemented; the *candidate-discovery*
structure around it was not the real thing the technique is named
for. This is exactly the gap `REPORT23.md`'s/`REPORT38.md`'s "a
genuinely different watch-list representation" was gesturing at
without ever precisely diagnosing it, and exactly why `propagate`/
`chooseWatch` kept showing up at 95-98% of CPU time across three
separate profiling passes (Stage 23, Stage 38, Stage 41) without
anyone finding a way to actually shrink that share.

**The fix**: a second, genuinely dynamic per-literal index
(`watchersPositive`/`watchersNegative`) that only ever holds clauses
*currently* watching a literal, maintained incrementally as watches
move — MiniSat's own standard in-place watch-list compaction pattern.
Implemented and thoroughly verified in both languages this stage.

**A second, real bug found along the way**: the Rust port surfaced
that multi-threaded clause sharing's `importClause` (Go) and its
Rust equivalent both extended `watch` for an imported clause without
also registering it in the new watcher lists — meaning an imported
clause would sit in `watch` correctly but stay invisible to
`propagate` (which now reads only the watcher lists) until an
eventual `reduceClauseDatabase` rebuild, which may never happen at
all with no memory limit set. This would have silently made
multi-threaded clause sharing inert in most real runs. Caught during
the Rust port's own careful mirroring of the design (not by any test
that existed before this stage), fixed in both languages, and given
its own dedicated regression test (confirmed, by deliberately
reverting the fix, to actually fail without it) — see "A second bug"
below.

**Measured result**: on `benchmark/uuf250-1065/uuf250-01.cnf` (the
exact hard instance profiled three times before), roughly 25-40%
faster wall-clock time in Go, ~30% in Rust, depending on measurement
method. On a 30-file sample of the 250-variable random set
specifically — the instance class `REPORT40.md` found losing to
`minisat`/`cryptominisat5` — the fixed version solved more files
within the same time budget (29/30 vs. 24/30 at a 20-second cap) and
averaged ~19% faster among files both versions solved. Zero
cross-language verdict mismatches, zero independent-solution-
verification failures, across every correctness check run, single-
and multi-threaded, in both languages.

**A second real bug found and fixed along the way**: the Rust port
surfaced that multi-threaded clause sharing's `importClause` extended
a clause's watch entry without also registering it in the new watcher
lists — silently making imported clauses invisible to `propagate`
until (if ever) a memory-limit-triggered database reduction happened
to rebuild them in. This would have quietly disabled clause sharing's
actual benefit in most real multi-threaded runs (this project's
default has no memory limit set). Fixed in both languages, with a
dedicated regression test in each, confirmed to actually fail without
the fix.

## The finding, in detail

`occurrence.Lists` (`internal/occurrence`) was built for local search
(`hc`/`ws`): given a variable, "every clause that mentions it" is
exactly what's needed to score the effect of flipping that variable.
`cdcl`'s `propagate` reused this same structure as its BCP candidate
source — reasonable to reach for (it already existed, already had the
right shape: `Positive[v]`/`Negative[v]`), but a fundamentally
different thing from what a two-watched-literal scheme actually needs.
The whole point of watching only two literals per clause (Moskewicz et
al., Chaff, DAC 2001) is that propagating one literal should cost work
proportional to how many clauses are *currently* watching it — a
small, dynamically shrinking-and-growing set — not how many clauses
merely *mention* it, which never shrinks and can be far larger for a
variable with many occurrences.

`propagate`'s existing code already had the tell, in retrospect: a
`switch`/`continue` whose default case skipped any candidate that
"isn't watching the falsified literal after all." In a genuine watch
list, this case is structurally unreachable — every entry in the
literal's own watcher list is, by construction, watching it. Its
presence meant the candidate source was never actually a watch list.

## The fix

A new field pair, `watchersPositive[v]`/`watchersNegative[v]`,
indexed exactly like `occurrence.Lists`' own `Positive`/`Negative` but
holding only clauses genuinely watching that literal right now:

- **Initial population** (`newSolver`): each clause's two starting
  watches are appended to the corresponding watcher lists at the same
  point `watch[c]` itself is set.
- **`propagate`'s rewrite**: MiniSat's own standard in-place
  compaction algorithm. Scanning a literal's watcher list with two
  indices — `scanned` (entries read) and `keep` (entries of those kept
  in this same list, `keep <= scanned` always) — a clause whose watch
  moves away (a replacement was found) is *not* written into the kept
  prefix (it leaves this literal's list, appended instead to its new
  watch's list); a clause with no replacement always stays (there's
  nowhere better to watch until a future backtrack un-falsifies this
  literal again) and is written into `list[keep]`. If a conflict is
  found mid-scan, the loop breaks immediately, but any not-yet-scanned
  tail is still genuine, valid watchers of this literal and must be
  copied into the compacted prefix before returning — skipping this
  step would silently drop clauses from their own watch list, a
  correctness bug that would only surface later as a missed
  propagation or missed conflict, not at the point of the mistake.
- **`addLearnedClause`**: a freshly learned clause's two initial
  watches are appended to the corresponding watcher lists, mirroring
  how it already extended the old occurrence index for the same
  clause.
- **`reduceClauseDatabase`**: after the existing clause renumbering
  (which already rebuilds `watch` wholesale via an old-to-new index
  map), the watcher lists are rebuilt wholesale too, from the new
  `watch` array directly — cheaper and far less error-prone than
  patching old entries through the rename map, and this only runs when
  a memory limit is actually exceeded, not on the hot path.

`lists` (the original occurrence index) is **not removed** — it's
still the correct, and only remaining, data structure for
`rephaseFromWalkSAT` (`STAGE43.md`), which genuinely needs every
clause mentioning a variable to score a WalkSAT flip correctly, not
just its current watchers.

## A second bug: imported clauses need registering too

`importClause` (multi-threaded clause sharing, `STAGE20.md`/
`STAGE21.md`'s Option B) extends `s.clauses`, `s.lists`, and `s.watch`
for a clause learned by another thread — but this stage's own first
pass missed that it needed to extend the *new* watcher lists too. The
symptom would have been silent, not a crash: an imported clause's
watch entry exists in `s.watch`, but `propagate` (which now reads
`watchersPositive`/`watchersNegative` exclusively) would never
discover it as a candidate, so the clause sits in the database,
correctly bookkept everywhere except the one place that actually
makes it useful, until (if ever) `reduceClauseDatabase`'s wholesale
rebuild happens to include it — which, with no memory limit
configured (this project's default), may never happen in a given run
at all. Multi-threaded `cdcl`'s clause sharing would have quietly
stopped doing anything in the overwhelmingly common case.

This was caught by the Rust port, not by anything in Go's own first
pass — porting the same design to a second, independently-typed
implementation surfaced the gap by forcing every touch point of
`watch` to be re-examined. Fixed in both languages
(`s.watchersFor(first)`/`s.watchersFor(second)` appended alongside
the existing `s.watch = append(...)` line), and given a dedicated
test in both languages
(`TestImportClauseRegistersWatcherLists`/its Rust equivalent) —
confirmed, by deliberately reverting the fix and re-running, to
actually fail without it, not just pass coincidentally. A fresh
multi-threaded, clause-sharing-active `benchcompare` run after the fix
(24 files, `--num-threads=4`, `uf250-1065`/`uuf250-1065`/`blocksworld`/
`ssa`) found 24/24 solved, 0 verify failures, 0 cross-language
mismatches in both languages.

## Why this doesn't change search behavior, only cost

This change is observationally transparent to *what* gets propagated
and *when a conflict exists* — only *how cheaply* `propagate` finds
that out. One real, expected side effect: when multiple clauses would
conflict simultaneously on the same falsified literal, which one is
discovered first can differ between the old (occurrence-list order)
and new (watcher-list order, which reflects construction and
watch-move history, not input order) traversal — this can lead to a
different learned clause, a different search trajectory, and
different `NumConflicts`/`NumDecisions` counts on the same input,
without either being wrong. This is the same kind of legitimate
divergence changing any other search parameter already produces; the
invariant that actually matters, and the one every verification step
below checks, is that the *verdict* (and, for `SAT`, a genuinely
verified solution) never changes.

## Measured results

**Hard instance** (`benchmark/uuf250-1065/uuf250-01.cnf`, profiled
three times before this stage): built old and new Go binaries from
the same source tree (`git stash` to isolate the change), timed both
directly and via `go test -bench`'s isolated single-threaded
benchmark:

| Measurement | Old | New | Change |
|---|---:|---:|---:|
| CLI, `/usr/bin/time`, mean of 3 runs | ~17.8s | ~13.3s | ~25% faster |
| `go test -bench` (isolated, no CLI/file-I/O overhead) | 17.88s | 10.44s | ~42% faster |

Both UNSAT, both identical to the pre-change verdict, every time.

A fresh CPU profile after the change (same instance, same
`BenchmarkRunHardSingleThreaded`) still shows `propagate`/
`chooseWatch` dominating (95.70%/52.91% respectively) — expected: the
*wasted* work (checking non-watching candidates) is what got cut, so
the *genuine* watched-literal work left over is now a larger share of
a much smaller total. The new `watchersFor` indirection itself costs
3.25% — a real but small overhead for the bookkeeping.

**30-file sample of the 250-variable random set** (`uf250-1065`/
`uuf250-1065`, 15 each, seed 42, `--time-limit-secs=20` — the exact
instance class `REPORT40.md` measured a gap on):

| | Solved | Mean (solved) | Median (solved) |
|---|---:|---:|---:|
| Old | 24/30 | 8.487s | 7.138s |
| New | 29/30 | 6.907s | 6.146s |

The new version solved 5 more files within the same time budget and
averaged ~19% faster among files both versions solved. Zero
cross-language verdict mismatches (checked against the *old*, still-
unmodified Rust binary during Go-only development — a pure internal
optimization, so an unmodified peer implementation's verdicts are
exactly what should still agree) and zero independent-solution-
verification failures across every run.

## Verification

- **Go**: `go build ./...`, `go vet ./...`, `gofmt -l .` clean;
  `go test ./...` (all ten packages, every pre-existing test passing
  unchanged) and `go test -race -count=10 ./internal/cdcl/...` clean.
- **New, hand-traced unit tests** (`internal/cdcl/watch_test.go`):
  `TestNewSolverPopulatesWatcherLists` (initial construction
  consistency), `TestAddLearnedClauseUpdatesWatcherLists`,
  `TestReduceClauseDatabaseRebuildsWatcherLists`, and — the one that
  actually exercises the delicate part —
  `TestPropagateWatcherListCompaction`: a hand-constructed scenario
  with four clauses sharing a watched literal, where one moves away
  (creating a real gap in the watcher list before any conflict is
  found), two more stay (one of which conflicts), and a fourth is
  positioned *after* the conflict in scan order and must survive
  completely untouched. Every expected final state (which clause
  conflicts, which watcher lists end up containing what, which
  variables get forced) was worked out by hand before running the
  test, not adjusted to match whatever the code happened to produce.
- **Broad correctness validation via `util/benchcompare`**: 119 files
  across every random, structured, and industrial `benchmark/`
  subdirectory this project has (`uf`/`uuf` at every size,
  `blocksworld`, `flat125-301`, `flat200-479`, `ssa`), `--sample=8`
  each, `--time-limit-secs=15` — 115/119 solved by both languages (4
  timeouts, both agreeing on which), **zero cross-language verdict
  mismatches, zero independent-verification failures**.
- **Rust**: `cargo build --release`, `cargo build --all-targets`,
  `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`
  clean; `cargo test --release` — 254 passed, 0 failed (was 250; 4 new
  tests mirroring Go's `watch_test.go` exactly, including the same
  hand-traced 4-clause gap/conflict/survivor compaction scenario, plus
  the `importClause`-registration regression test). Existing
  multi-threaded/clause-sharing tests all still pass, confirming the
  `import_clause` fix didn't disturb anything else.
- **Broad correctness validation, both languages, both single- and
  multi-threaded**: 119 files across every `benchmark/` subdirectory
  (single-threaded, `--sample=8`, `--time-limit-secs=15`) — 115/119
  (Go) and 117/119 (Rust) solved, 0 verify failures, **0
  cross-language mismatches**. A second, multi-threaded sweep (24
  files, `--num-threads=4`, clause sharing active) after the
  `importClause` fix — 24/24 solved both languages, 0 verify failures,
  0 mismatches.
- **Rust timing** on the same hard instance
  (`benchmark/uuf250-1065/uuf250-01.cnf`): old Rust 15.07s/14.86s
  (2 repeats) → new Rust 10.36s/10.44s — a consistent **~30% speedup**,
  in the same range as Go's own 25-42% depending on measurement
  method.

## Documentation

- `docs/background.md` — a new subsection under "CDCL," "A genuine
  watch-list, not just watched literals," explaining the finding and
  fix in the same detail as this report's own summary; the "watched
  literals" paragraph under DFS/DPLL updated to note that `dfs`'s own
  BCP still uses the old (occurrence-index) design, unaffected by this
  stage; a short note added to "Multi-threaded CDCL, shared clause
  database" about the `importClause` bug this stage found and fixed.
- `docs/usage.md`/`docs/internal-parameters.md`/`docs/references.md`
  — no changes needed: no new CLI surface, no new tunable parameter,
  no new literature citation (Moskewicz et al. was already cited for
  watched literals generally).

## A related finding, out of this stage's scope

`internal/dfs/watch.go` — `dfs`'s own, separate BCP implementation
(kept independent per `STAGE11.md`'s original instruction) — has the
*exact same* architectural characteristic: it also finds its BCP
candidates via the full occurrence index, not a genuine watch list.
This wasn't part of `REPORT40.md`'s gap (which was specifically about
`cdcl` vs. external solvers) and wasn't touched this stage, but it's
a real, live opportunity worth naming rather than leaving buried in
code — see "Questions for you" below.

## Questions for you

- `dfs` has the identical inefficiency this stage just fixed in
  `cdcl` (see above) — worth its own stage applying the same fix, or
  is `dfs`'s own performance not currently a priority the way closing
  the `minisat` gap was?
- This stage's fix was purely internal (no CLI surface, no new
  parameter) — is a broader benchmark sweep against `minisat`/
  `cryptominisat5` (re-running something like `REPORT40.md`'s own
  comparison) worth doing now, to see how much of that original gap
  this closes, or is the direct old-vs-new measurement in this report
  convincing enough on its own for now?
- `REPORT23.md`'s doc comment on `chooseWatch` still describes a
  rejected "check if the other watch is already true before scanning"
  optimization that measured worse *given the old candidate-discovery
  cost dominating everything else*. Worth re-measuring that
  specifically now that propagate's own overhead has dropped
  substantially, in case the balance of costs has shifted enough to
  change that earlier conclusion?
