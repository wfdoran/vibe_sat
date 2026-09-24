# Report: Stage 48

## Summary

`STAGE48.md` asked for a fresh CPU profile of `cdcl`'s `propagate` on
the current, post-Stage-45/46 code, since the last profile
(`REPORT41.md`) predates the watch-list fix and is now stale.

**The profile turned up an obvious fix, and it was made**: with
`propagate`'s own candidate-discovery cost fixed (Stage 45),
`chooseWatch`'s own linear scan for a replacement watch is now 49.59%
of total CPU time on its own — no longer a cheap scan riding along for
free inside a much bigger cost, but close to half the total. This
directly revived a specific optimization `REPORT23.md` tried and
*rejected* early in this project: the "blocking literal" check — skip
`chooseWatch` entirely when a clause's other watch is already true,
since an already-satisfied clause needs no attention until some future
backtrack. `REPORT23.md` measured this making things worse under the
old candidate-discovery design; **re-measured against the new one, it
measures as a clear, consistent win everywhere tested**: ~32% faster
on this project's standing hard benchmark instance, ~15% faster mean
time across 180 varied smaller files, ~22% faster mean time across a
250-variable random sample, no correctness regressions anywhere, in
either language.

This is a genuine reversal of a previously-published conclusion, not a
new discovery — the technique never changed; what it was competing
against did, once Stage 45 changed `propagate`'s own cost structure.

## The profile

`BenchmarkRunHardSingleThreaded` (`internal/cdcl`'s own standing
benchmark, `benchmark/uuf250-1065/uuf250-01.cnf`), 5 reps, current
(post-Stage-45/46) code:

| Function | flat% | cum% |
|---|---:|---:|
| `propagate` | 41.66% | 96.40% |
| `chooseWatch` (inline) | 29.75% | **49.59%** |
| `cnf.Literal.Var` (inline) | 11.67% | 11.67% |
| `isFalse` (inline) | 9.21% | 20.18% |
| `watchersFor` (inline) | 3.16% | 4.48% |

Line-level detail on `propagate` surfaced a second, smaller
observation worth recording even though it wasn't acted on this stage:
the append that moves a clause into its new watch's list
(`*s.watchersFor(replacement) = append(...)`) costs ~12% of total time
on its own — not from slice *reallocation* (`runtime.growslice` was a
negligible 0.57% of total), just the ordinary cost of the function
call, pointer indirection, and append bookkeeping happening on every
single watch move, which is frequent. Not `chooseWatch`-sized, but a
real, measured cost with no obvious quick fix (see "Further
opportunities" below).

## The fix: reviving the blocking-literal check

A new `isTrue` helper (the mirror image of the existing `isFalse`),
and one new branch inside `propagate`'s scan loop: right after
determining a clause's other watch, and *before* calling `chooseWatch`
to look for a replacement, check whether that other watch is already
true. If so, the clause is already satisfied — keep it watching the
now-falsified literal (exactly like the existing "no replacement
found" case already does for the compacted-list bookkeeping) and move
on, without ever calling `chooseWatch` or checking for a forced
propagation, since there is nothing to force or conflict on when the
clause is already known-satisfied.

### Why `REPORT23.md`'s rejection doesn't hold anymore

`REPORT23.md`'s own reasoning at the time: this project's clauses are
mostly length 3, so `chooseWatch`'s scan was *already* cheap (checking
one remaining literal), leaving little for the added check to skip,
while costing a real branch and an extra `Literal.Var()` call on
*every* candidate regardless of payoff. That reasoning was sound
*given the candidate source in place at the time* — `propagate` was
visiting every occurrence of a literal, not just its genuine watchers,
so `chooseWatch` was called far more rarely relative to total work,
and its own already-cheap scan had little room to be made cheaper by
skipping it sometimes. Once Stage 45 fixed candidate discovery so
`propagate` only ever visits genuine watchers, `chooseWatch` gets
called on every single one of them — turning its "already cheap" scan
into the largest single cost in the profile, and turning "skip it when
possible" from a marginal, unreliable win into an unconditional one.

## Measured results

**Hard instance** (`benchmark/uuf250-1065/uuf250-01.cnf`), old
(current committed code) vs. new (with the blocking-literal check),
built from the same source tree via `git stash`:

| Run | Old | New | Change |
|---|---:|---:|---:|
| CLI wall-clock, repeat 1 | 13.36s | 9.14s | |
| CLI wall-clock, repeat 2 | 13.28s | 9.23s | |
| CLI wall-clock, repeat 3 | 13.42s | 9.27s | **~31.6% faster** |

Both UNSAT, matching the pre-change verdict, every time.

**Broad correctness + timing validation via `util/benchcompare`**:

- 180 files across every random/structured `benchmark/` subdirectory
  up to 175 variables (`--sample=15`, `--time-limit-secs=15`) — all
  180 solved by both old and new, 0 verify failures, **0
  cross-language mismatches** (using old/new as the two "languages"):
  mean time 0.048s → 0.041s (**~15% faster**), median 0.008s → 0.007s.
- 30 files, 250-variable random set specifically (`--sample=15`,
  `--time-limit-secs=20`) — 29/30 solved by both (1 timeout, same
  file, both versions), 0 verify failures, **0 mismatches**: mean time
  7.020s → 5.490s (**~21.8% faster**), median 6.435s → 6.021s.
- Multi-threaded (`--num-threads=4`, clause sharing active), 24 files
  — 24/24 solved both, 0 verify failures, **0 mismatches**: mean time
  2.035s → 1.502s.

**Rust** (`uuf250-01.cnf`, 2 repeats): old Rust 10.77s/10.88s → new
Rust 8.21s/8.04s (**~24-26% faster**), same direction as Go's ~32%.
Same broad `util/benchcompare` sweeps (180 files, 30 files) — both
solved counts unchanged, 0 verify failures, **0 cross-language
mismatches** in either sweep.

## Verification

- **Go**: `go build ./...`, `go vet ./...`, `gofmt -l .` clean;
  `go test ./...` (all ten packages, every pre-existing test passing
  unchanged) and `go test -race -count=10 ./internal/cdcl/...` clean.
- **New test** (`internal/cdcl/watch_test.go`):
  `TestPropagateSkipsChooseWatchForBlockingLiteral` — a single clause
  `{1, 2, 3}` with initial watches `{1, 2}`; variable 2 set true
  directly (pure setup), variable 1 set false via a real `propagate()`
  call. Asserts the watch is *still* `{1, 2}` afterward, not moved to
  `{3, 2}` — literal 3 is unassigned and would be a valid replacement
  if `chooseWatch` were actually called, so this is the definitive
  proof the blocking-literal check fired. Confirmed to actually fail
  (watch moves to `[3 2]`) when re-run against the pre-fix code, before
  restoring the fix and confirming it passes.
- **Rust**: `cargo build --release`/`--all-targets`, `cargo fmt --check`,
  `cargo clippy --all-targets -- -D warnings` clean; `cargo test
  --release` — 258 passed, 0 failed (was 257; one new test,
  `test_propagate_skips_choose_watch_for_blocking_literal`, mirroring
  Go's exactly). The Rust test's teeth were confirmed the same way:
  temporarily removing the new early-continue branch made it fail with
  the identical failure mode (`watch[0] = [3, 2]`, want unchanged
  `[1, 2]`), restoring the fix made it pass again.

## Further opportunities (not acted on this stage)

The profile surfaced two more candidates, neither pursued here since
neither is as clearly "obvious" as the blocking-literal check:

1. **The append-on-watch-move cost** (~12% of total, see "The
   profile" above). No quick fix identified — it isn't reallocation
   overhead, just the ordinary cost of a frequent operation. A real
   fix would likely mean a different watcher-list data structure
   entirely (e.g. an intrusive linked structure avoiding the
   append/compact pattern altogether), which is a much bigger
   undertaking than this stage's scope, not a quick win.
2. **`isFalse`/`Literal.Var()`'s combined ~20-32% share** is mostly
   the unavoidable cost of checking an assignment array by variable
   index — real, but not obviously reducible without a more invasive
   representation change (e.g. packing sign into the assignment
   array's own indexing scheme to avoid a separate sign check).

Neither is recommended as a near-term next step; they're recorded here
so a future profiling pass doesn't have to rediscover them from
scratch.

## Documentation

- `docs/background.md` — new "The blocking-literal check: a rejected
  optimization, reversed" subsection under CDCL; the "Other things
  worth knowing" bullet citing the original rejection updated to
  record the reversal too, framed as a further example of this
  project's measurement culture (a measurement can be revisited, not
  just trusted forever, when the surrounding code changes).
- `docs/usage.md`/`docs/internal-parameters.md`/`docs/references.md`
  — no changes needed: no new CLI surface, no new tunable parameter
  (the blocking-literal check is unconditional, not `--alg-params`-
  selectable, matching how `STAGE45.md`'s watch-list fix itself was
  also unconditional), no new literature citation (already covered by
  the existing Chaff citation).

## Questions for you

- The two "further opportunities" above are real but not obviously
  quick wins — worth a dedicated future stage, or lower priority than
  whatever's next on your list?
- Given this reversal, is it worth a quick pass re-checking any other
  previously-rejected optimization whose rejection was measured
  *before* Stage 45's fix, in case the same "what it was competing
  against changed" logic applies elsewhere too? I'm not aware of one
  offhand, but I also wasn't specifically looking before this stage's
  profile prompted it.
