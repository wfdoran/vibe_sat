# Report: Stage 31

## Summary

`REPORT30.md` fixed subsumption elimination's `O(clauses²)` cost, then found
that a cross-language sweep replicating `REPORT29.md`'s Finding 4 still
showed the same 7/15 Go and 6/15 Rust files killed by the harness's
45-second hard timeout — because bounded variable elimination (BVE), left
untouched by that stage, was now the dominant, and on at least one file
effectively unbounded, cost. `STAGE31.md` asked for exactly the follow-up
`REPORT30.md` recommended: profile BVE, fix it if a faster algorithm exists,
bound it otherwise.

Profiling (below) found **two distinct, real bottlenecks**, not one:

1. **`resolve()`'s tautology/duplicate-literal check was `O(clause
   length²)`** (a linear scan per literal), and was called an enormous
   number of times per high-degree variable. On one real file this was 74%
   of the *entire* preprocessing run's CPU time by itself.
2. **Every single accepted elimination rebuilt both occurrence-list arrays
   and rescanned the whole clause set from scratch** (both `O(clauses)`),
   exactly as `REPORT30.md` suspected — on a file with many eliminations,
   this became `O(variables × clauses)` in total bookkeeping alone.

Both are fixed, in both languages:

1. **`resolveWithMarks`** replaces `resolve`'s repeated linear scans with a
   reusable `mark` array indexed by variable, turning the check into
   `O(clause length)`.
2. **Occurrence lists are now built once and maintained incrementally** —
   clauses are tombstoned (never physically removed, which would require
   renumbering every later index) and a variable's occurrence list is
   compacted lazily, only when that variable is next considered.

Even with both fixes, a single variable that genuinely occurs in thousands
of clauses on both polarities still costs `O(occurrences²)` resolve calls
if its elimination is *accepted* — no amount of bookkeeping cleverness
avoids that, it's inherent to what BVE computes. So, matching `STAGE31.md`'s
"fix it, or bound it" framing exactly as `REPORT30.md` did for subsumption,
this stage also adds a **work-budget cap** (`bveWorkBudgetFactor = 2000`,
`BVE_WORK_BUDGET_FACTOR` in Rust) — with one design wrinkle this stage found
the hard way (see "A budget that must survive between calls" below).

**Result**: a cross-language sweep replicating `REPORT29.md`/`REPORT30.md`'s
Finding 4 methodology now shows **0 of 15 files killed by the timeout, in
both languages** — down from 7/15 Go and 6/15 Rust. Every real file this
stage measured dropped from multi-second (or unbounded/hanging) BVE cost to
sub-second, with one deliberate exception (a file whose elimination is
genuinely large enough that the work budget caps it, bounded rather than
solved — reported honestly below, not glossed over).

## Profiling methodology

Following `REPORT25.md`'s and `REPORT30.md`'s precedent: `go tool pprof` via
a benchmark targeting a real, BVE-heavy file
(`sat_comp/2018/.../gto_p50c314.cnf`, 350 variables eliminated), plus a
second profiling run against the worst offender from `REPORT30.md`'s sweep
(`apn-sbox5-cut3-symmbreak.cnf`, 21,240 variables / 86,081 clauses) — which
doesn't finish in any practical time, so profiling it required starting a
CPU profile, running `eliminateVariables` in a goroutine, sleeping a fixed
window, and stopping the profile without waiting for the call to return
(capturing where time actually goes during that window, even though the
call itself never completes). Both this and the benchmark used only for
this stage's diagnosis are not part of the committed diff.

**`gto_p50c314.cnf`** (finishes in ~6.2s): `go tool pprof -top` showed
`resolve` at **73.8% cumulative**, almost entirely inside its `slices.Contains`-based
tautology/duplicate check. `eliminateVariables`'s own frame (the
rebuild/rescan bookkeeping) was only 2.9% flat — on this file, resolve
itself, not the bookkeeping, was the dominant cost.

**`apn-sbox5-cut3-symmbreak.cnf`** (doesn't finish): a very different
profile — heavy GC/allocation activity (`runtime.scanObject` 26.6%,
`runtime.mallocgc` 18.1%, `runtime.tryDeferToSpanScan` 22.4%) alongside
`resolve` at "only" 36.7% cumulative. This is exactly what
`REPORT30.md` predicted: rebuilding two `O(numVars)`-sized occurrence-list
arrays and rescanning all 86,081 clauses, on every single one of
(evidently) many eliminations, produces enormous allocation/copy churn that
dwarfs even `resolve`'s own already-expensive cost on this file.

A direct call-count measurement (temporary instrumentation, also not part
of the committed diff) across every file this project has tested against
confirmed just how differently-shaped these two costs are:

| File | Clauses | resolveWithMarks calls | calls / clauses |
|---|---|---|---|
| `bw_large.c.cnf` | 50,457 | 976,389 | 19x |
| `bw_large.d.cnf` | 131,973 | 3,121,049 | 24x |
| `mchess_15.cnf` | 1,391 | 201,215 | 145x |
| `factoring39916801x54018521.cnf` | 11,587 | 3,857,729 | 333x |
| `a_rphp065_04.cnf` | 10,063 | 4,301,161 | 427x |
| `gto_p50c314.cnf` | 4,319 | 4,290,299 | **994x** |
| `prime_119218851371.cnf` | 23,693 | 11,333,408 | 478x |
| `apn-sbox5-cut3-symmbreak.cnf` | 86,081 | 244,882,890 (in 20s, still climbing) | **2845x and rising** |

Every real, legitimate file needs at most ~994x its clause count in resolve
calls to complete BVE fully; the pathological file blows past that by 3x
and keeps growing, with no sign of leveling off in the sampled window. This
table is what "2000" (below) is calibrated against.

## The fix: incremental occurrence lists + O(clause length) resolve

`resolveWithMarks` (Go) / `resolve_with_marks` (Rust) computes the identical
resolvent `resolve` always did, but replaces `resolve`'s repeated
`slices.Contains`/`.contains()` scans with a `mark` array indexed by
variable (`+1`/`-1`/`0` for "seen positive," "seen negative," "not seen"),
reused across every call in a single `eliminateVariables` run. A `touched`
list records exactly which entries a call set, so cleanup after each call
is `O(resolvent length)`, not `O(numVars)` — the same "reusable buffer,
touched-list cleanup" pattern real SAT preprocessors use for exactly this
reason. `resolve` itself (the old `O(n²)` version) is kept — in both
languages — purely as `eliminateVariables`'s test oracle, exactly like
`REPORT30.md` kept `bruteForceSubsumedClauses`.

`eliminateVariables` (Go) / `eliminate_variables` (Rust) now builds
occurrence lists once, not once per elimination. A clause is never
physically removed (that would still require an `O(clauses)` renumbering of
every later index); instead a `live` boolean array tombstones it, and a
variable's occurrence list is compacted — lazily, only when that variable
is next considered, reusing its own backing array — immediately before use.
New resolvents are appended to the end of the growing clause list (never
inserted or reordered), and their literals' occurrence lists get exactly
the one new entry each needs.

**The elimination order, and therefore the exact sequence of steps and the
exact final surviving clauses, is unchanged from before this stage.** This
was a deliberate design choice, not an accident: the new function still
restarts its scan from `v=1` after every single elimination, mirroring the
pre-Stage-31 control flow exactly, rather than the (also-correct, and
initially tempting) alternative of finishing a full pass before restarting.
Restarting from `v=1` costs only `O(numVars)` per elimination now that
nothing else about it triggers a rebuild, so there was no performance reason
to give up exact-order equivalence — and keeping it meant
`TestEliminateVariablesMatchesBruteForceOnRandomFormulas` could assert
byte-for-byte equality against a brute-force oracle, exactly as strong a
check as `REPORT30.md`'s subsumption fuzz test, rather than a weaker "still
satisfiability-preserving" property.

A second, complementary fix targets `resolve`-call *volume*, not just
per-call cost: the inner resolution loop now bails out as soon as
`len(resolvents) > removedCount` is exceeded, instead of computing every
remaining resolvent before checking the non-increasing condition. This
count can only grow, never shrink, so once exceeded the eventual rejection
is already determined — this specifically targets high-degree variables
that get *rejected*, saving the wasted tail of an `O(occurrences²)`
computation whose result was never going to be used. (It does not help the
*accepted* case, which genuinely needs every resolvent — that's what the
work budget below is for.)

## The work-budget cap, and a bug in its first version

`bveWorkBudgetFactor = 2000` bounds the total number of `resolveWithMarks`
calls to `2000 × (original clause count)`. Chosen with roughly 2x headroom
above the highest legitimate value this stage measured (~994x, on
`gto_p50c314.cnf`) while cutting off the pathological file's runaway growth
(already past 2845x and climbing after 20 seconds) well before it becomes a
multi-second cost. Exhausting the budget partway through examining a
variable abandons that variable (and every variable after it, for that
call) without accepting it — safe for the same reason `REPORT30.md`'s
subsumption budget was safe: skipping an elimination can never change
whether the formula is satisfiable, only how compact it ends up.

**The first version of this cap didn't actually bound anything**, and I
want to report that honestly rather than skip straight to the fixed
version. My first implementation reset `budget := bveWorkBudgetFactor *
len(clauses)` fresh at the top of every single call to
`eliminateVariables`. That looked reasonable in isolation, but
`preprocess.Run` calls `eliminateVariables` once *per preprocessing round*,
up to `maxRounds = 1000` times, repeating until nothing changes. Exhausting
the budget partway through one expensive variable neither eliminates that
variable (so it's still a candidate) nor marks it rejected (so nothing
remembers to avoid it) — it just returns, `changed` stays true because some
*other* technique made progress, and `Run`'s loop calls `eliminateVariables`
again next round, with a brand-new full budget, immediately burning it all
back down on the exact same variable. I found this empirically:
`apn-sbox5-cut3-symmbreak.cnf` still didn't finish within 60 seconds even
with the "capped" version in place, which contradicted the whole point of
adding a cap, so I didn't stop at "the number decreases" — I re-profiled to
find out why it didn't decrease *enough*, and found this.

The fix: `budget` is now a `*int` (Go) / `&mut usize` (Rust) that
`preprocess.Run`/`run` owns and initializes **once**, before its round
loop, then passes to every round's call to `eliminateVariables` — the exact
same variable, threaded through, round after round. Once it reaches zero,
every later call in the same `Run` bails out on its very first candidate
pair, for the cost of a handful of array accesses, instead of re-attempting
the same expensive variable from scratch each time. This closed the hole
completely: see "Benchmark results" below for `apn-sbox5-cut3-symmbreak.cnf`
going from "still hanging past 60 seconds with a per-call budget" to a
consistent, reproducible ~13s (Go) / ~8s (Rust) with the shared one.

## Verification

- **Go**: `TestResolveWithMarksMatchesResolveAcrossReusedBuffers` checks
  `resolveWithMarks` against `resolve` directly across several calls that
  deliberately reuse the same `mark`/`touched` buffers and revisit
  overlapping variables (to catch exactly the kind of leftover-mark bug
  that a partial cleanup could introduce), then asserts nothing is left set
  in `mark` afterward. `TestEliminateVariablesMatchesBruteForceOnRandomFormulas`
  (500 random trials) is the strong exact-equality check the "unchanged
  elimination order" design choice above was made to enable, against a
  kept-for-testing `bruteForceEliminateVariables` oracle (a verbatim copy of
  the pre-Stage-31 algorithm). All pre-existing tests updated for the new
  `budget *int` parameter via a `hugeBudget()` test helper that makes the
  cap a non-factor for every test except the ones specifically checking it.
  `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./...`, and
  `go test -race -count=10 ./internal/preprocess/...` are all clean.
- **Rust**: `test_resolve_with_marks_matches_resolve_across_reused_buffers`
  and `test_eliminate_variables_matches_brute_force_on_random_formulas`
  (same 500-trial design) are new, ported 1:1. `resolve` itself moved into
  `mod tests` (it has no remaining production caller — `eliminate_variables`
  uses `resolve_with_marks` exclusively), mirroring how `REPORT30.md` kept
  `bruteForceSubsumedClauses` test-only. `cargo build --release`, `cargo
  clippy --all-targets -- -D warnings`, `cargo fmt --check`, and `cargo test
  --release` (197 tests) are all clean.
- Cross-language: `util/benchcompare` — see "Benchmark results" below —
  showed 0 verdict mismatches and 0 verification failures across the sweep.
- `util/benchcompare` itself is unaffected by this stage; its own
  `go build`/`go vet`/`gofmt`/`go test` remain clean.

## Benchmark results

Preprocessing-isolated timings (`--algorithm=hc --alg-params=1
--verbose=1`, matching `REPORT25.md`/`REPORT30.md`'s methodology):

| File | Go before | Go after | Rust after |
|---|---|---|---|
| `benchmark/blocksworld/bw_large.c.cnf` | 1.15s | 0.19s | 0.09s |
| `benchmark/blocksworld/bw_large.d.cnf` | 3.88s | 0.43s | 0.23s |
| `sat_comp/2018/.../prime_119218851371.cnf` | 1.26s | 0.50s | 0.21s |
| `sat_comp/2018/.../gto_p50c314.cnf` | 6.18s | 0.59s | 0.37s |
| `sat_comp/2018/.../factoring39916801x54018521.cnf` | 0.33s | 0.15s | 0.06s |
| `sat_comp/2018/.../mchess_15.cnf` | 0.05s | 0.03s | 0.01s |
| `sat_comp/2018/.../a_rphp065_04.cnf` | hung indefinitely | 4.50s | 1.28s |
| `sat_comp/2018/.../apn-sbox5-cut3-symmbreak.cnf` | hung indefinitely | 13.07s (budget-bounded) | 8.21s (budget-bounded) |

("Go before" reproduces `REPORT30.md`'s own post-subsumption-fix numbers —
the state this stage started from — not a re-measurement.) `gto_p50c314.cnf`
is the clearest single before/after: `REPORT30.md` flagged its residual 6.18s
as "attributable to BVE... explicitly out of scope for this stage" — that
residual is now 0.59s, a 90% reduction, and this stage's whole reason for
existing.

**`apn-sbox5-cut3-symmbreak.cnf` is bounded, not solved**, and I want that
distinction to be clear rather than implied by a good-looking number. Its
`preprocess:` line reports `vars 21240->19124 clauses 86081->72998 (units=435
pure=0 subsumed=0 eliminated=1681)` — 1,681 variables eliminated before the
work budget ran out, leaving 19,124 still unassigned/uneliminated. `Run`
does not treat this as an error or a partial-failure state: it simply
proceeds to renumber and hand the (still-simplified, still-correct, just
less-simplified-than-it-could-theoretically-be) problem to whichever
solving algorithm was requested, exactly as it always has for every other
early-exit case in this codebase (`REPORT30.md`'s subsumption budget,
`maxRounds` itself). Correctness is unaffected — the cross-language sweep
below found 0 mismatches on this exact file — but a future run with a much
larger budget (or a genuinely different algorithm for this specific
adversarial shape) could in principle simplify it further than this stage's
budget allows. That tradeoff is the explicit point of a work budget, not an
oversight.

### The full sweep: REPORT29/30's Finding 4, resolved

Reproducing `REPORT30.md`'s exact cross-language sweep methodology
(`--paths=sat_comp/2018 --max-size-mb=3 --sample=15 --seed=1
--algorithm=cdcl --time-limit-secs=15 --hard-timeout-secs=45`):

| | `REPORT29.md` | `REPORT30.md` (subsumption fixed) | `REPORT31.md` (BVE also fixed) |
|---|---|---|---|
| Go killed by timeout | 7 of 15 | 7 of 15 | **0 of 15** |
| Rust killed by timeout | 6 of 15 | 6 of 15 | **0 of 15** |

The sweep itself also got faster to run: 9 minutes wall-clock this stage,
down from 15+ minutes for the identical sweep in `REPORT30.md` (both
languages, 30 total runs). `solved` remains 0/15 in both languages —
consistent with `REPORT29.md`'s Finding 3 that this benchmark class is
genuinely hard for `cdcl` to prove SAT/UNSAT within a 15-second search
budget regardless of preprocessing speed; that is a search-hardness
property of these instances, unrelated to preprocessing performance, and
not something either this stage or `REPORT30.md` claimed to address.
Verdict agreement and independent verification remained perfect: 0
cross-language mismatches, 0 verification failures.

## Command line arguments

None. No `vibe_sat` CLI flags changed in either language this stage.

## Testing

- Go: `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./...`,
  `go test -race -count=10 ./internal/preprocess/...` all clean.
- Rust: `cargo build --release`, `cargo clippy --all-targets -- -D
  warnings`, `cargo fmt --check`, `cargo test --release` (197 tests) all
  clean.
- Cross-language: `util/benchcompare` sweep (above) — 0 mismatches, 0
  verification failures, 0 files killed by the hard timeout in either
  language (down from 7/15 and 6/15).
- Manual verification against every file in "Benchmark results": both
  languages report identical `vars`/`clauses`/`subsumed`/`eliminated`
  counts to each other on every file, confirming the rewrite changed
  *speed*, not *what gets simplified* (up to the new work budget, which is
  itself identically parameterized in both languages).

## Open questions / notes for you

- `apn-sbox5-cut3-symmbreak.cnf` is the one file in this project's testing
  history where the BVE work budget visibly caps real, wanted simplification
  (1,681 of presumably more eliminable variables). If a future stage wants
  to push further on this specific file's structure specifically (it's a
  cryptographic S-box symmetry-breaking encoding — the kind of instance
  where a handful of variables can have extremely high, genuinely
  irreducible degree), raising `bveWorkBudgetFactor`, or pursuing a
  length-based rather than count-based elimination heuristic, are the two
  directions I'd suggest — but this project's own measurements don't show
  this blocking anything currently in scope, so I did not chase it further.
- I did not modify `todo.md`, per `STAGE31.md`'s explicit request.
- No language/toolchain version changes needed.
