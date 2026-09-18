# Report: Stage 34

## Summary

`STAGE34.md` asked for the top item on `REPORT33.md`'s ranked
backlog: **LBD-based clause management**, Glucose-style (Audemard &
Simon, "Predicting Learnt Clauses Quality in Modern SAT Solvers,"
IJCAI 2009). Done in both languages:

- Every learned clause now gets a **Literal Block Distance** (LBD) —
  the number of distinct decision levels among its literals — computed
  in the same pass `analyze` already uses to derive `backtrackLevel`,
  at no extra traversal cost.
- `reduceClauseDatabase`'s deletion policy now sorts by LBD first
  (highest LBD deleted first) with activity only a tiebreak among
  equal LBDs, and never deletes a "glue" clause (LBD ≤ 2) regardless
  of activity.
- A new fifth restart strategy, `--alg-params val2=5`
  (`RestartGlucose`/`RestartStrategy::Glucose`), restarts when a
  50-conflict moving average of recent LBDs looks close to or worse
  than the all-time average, instead of a fixed conflict-count
  schedule.

While benchmarking the new restart strategy, its much higher restart
frequency surfaced a real, previously-latent crash in `maybeRestart`
(present since Stage 15, in both languages) — see "A bug found during
benchmarking" below. It's fixed, with a regression test in each
language.

The benchmark comparison itself is a **negative result**: on this
project's own benchmark mix, `val2=5` solved slightly *fewer*
instances within a fixed time budget than the existing default
(`val2=2`, polynomial), not more. `RestartGlucose` is implemented,
correct, and available, but is **not** made the default — consistent
with this project's now well-established pattern (VSIDS over LRB,
polynomial over Luby) of trusting this project's own measurement over
a technique's literature reputation. See "Benchmark results" below.

Per `STAGE34.md`'s new standing instruction, `docs/references.md`,
`docs/usage.md`, and `docs/background.md` are all updated this stage
(see "Documentation" below), and updating them is now a permanent part
of every future stage's checklist.

## Design

### LBD computation (`analyze`)

`analyze`'s first-UIP resolution loop already visits every literal
that ends up in the learned clause; LBD is just "how many distinct
`level[]` values did we see," so it's computed in the same pass rather
than a second walk over the finished clause. A reused scratch buffer
(`s.lbdScratch` in Go, `lbd_scratch` in Rust — the same
reused-buffer-beats-reallocation convention this project has used
since Stage 30/31/32's own work) is seeded with `s.currentLevel` (the
conflict's own level always counts, even though the loop below only
ever sees strictly *lower* levels for literals kept in the learned
clause) and grown with `slices.Contains`/`Vec::contains` — a linear
scan, deliberately, since a learned clause's `result[1:]` and its
distinct-level count are both small in practice; the same
"linear-scan-beats-a-map-for-small-N" call this project has made
before. `analyze` now returns `(learned, backtrackLevel, lbd)` instead
of `(learned, backtrackLevel)`.

### Clause-database management (`reduceClauseDatabase`)

Two changes, both described in the package/module doc comments and in
`reduceClauseDatabase`'s own:

1. **Glue-clause protection**: `eligible` now excludes any clause with
   `clauseLBD[idx] <= glueClauseLBDThreshold` (2) up front, regardless
   of activity — a glue clause is never a deletion candidate at all,
   the same way a locked clause never is.
2. **LBD-primary sort**: among the clauses that remain eligible, the
   sort key is now LBD descending (highest LBD — least "compact" —
   deleted first) with the old activity-ascending comparison used only
   to break ties among clauses with equal LBD.

`clauseLBD` (`clause_lbd` in Rust) is threaded everywhere `clauseActivity`
already was: `newSolver`'s bootstrap (original clauses get LBD 0, a
placeholder that's never read since `reduceClauseDatabase` never
considers indices `< numOriginalClauses`), `addLearnedClause` (now
takes an `lbd` parameter), and `reduceClauseDatabase`'s rebuild.

**The clause-import path needed the same treatment and initially
didn't have it.** `clauseshare.go`'s `importClause`
(`clause_share.rs`'s `import_clause`) appends directly to
`s.clauses`/`s.clauseActivity` for a clause learned by another thread,
bypassing `addLearnedClause` entirely — this was caught while auditing
every append site, before it could become an index-out-of-range panic
the first time `reduceClauseDatabase` ran after any parallel import.
Since the exporting thread's true LBD was computed against *its own*
decision levels (meaningless here), imported clauses get
`len(clause)` as an LBD estimate: LBD can never exceed a clause's
literal count (each literal contributes at most one distinct level),
so this is always conservative, and for the many exported binary
clauses (`exportMaxClauseLen` allows up to length 8, but short clauses
dominate in practice) it's exactly correct, not just an upper bound,
since a 2-literal clause's LBD is trivially ≤ 2 either way.

### Restart policy (`RestartGlucose` / `RestartStrategy::Glucose`)

New solver-level bookkeeping (`GlucoseState` in Rust; a handful of
fields directly on `solver` in Go, documented as "never reset,
including across restarts" for the all-time running average):

- A fixed-size ring buffer of the last `glucoseWindowSize` (50)
  learned clauses' LBDs, kept as an incrementally-updated sum
  (`recordLBD`/`record_lbd` is O(1) regardless of window size).
- An all-time running sum/count of every LBD ever recorded.

`glucoseShouldRestart`/`glucose_should_restart` requires a full recent
window before ever triggering (an empty/partial ring buffer isn't a
meaningful average yet), then restarts when
`recentAvg * glucoseK >= globalAvg` (`glucoseK = 0.8`, the literature's
usual choice, kept absent this project's own evidence favoring
something else — the same "trust the literature until measurement says
otherwise" default this project applied to the Luby base constant
before its own benchmark override).

`maybeRestart`/`maybe_restart` special-cases `RestartGlucose`: it
doesn't consult `restartThreshold`/`conflictsSinceRestart`'s count
against a precomputed number at all, only the data-driven comparison
above. `RestartGlucose` is not part of `RestartRoundRobin`'s rotation
(unchanged: quadratic/geometric/Luby by worker index).

## A bug found during benchmarking

`benchcompare` runs across `blocksworld` under `--alg-params 2 5`
crashed on two files, in **both** languages:

```
panic: runtime error: index out of range [1] with length 1
        cdcl.(*solver).backtrackTo(...)
        cdcl.(*solver).maybeRestart(...)
```

```
thread 'main' panicked at src/cdcl/mod.rs:1671:24:
index out of bounds: the len is 1 but the index is 1
```

**Root cause**: `backtrackTo(0)` indexes `trailLim[1]`
(`trail_lim[1]` in Rust) — a slot that only exists once
`currentLevel > 0`. Immediately after a conflict resolves to a
*level-0 learned unit clause*, `learnAndBackjump`'s own
`backtrackTo(backtrackLevel)` call already leaves `currentLevel` at 0
before `maybeRestart` is even called. `maybeRestart` had no guard for
this: if a restart happened to be due at exactly this moment, it would
call `backtrackTo(0)` a second time with nothing left to undo, and
panic.

**This bug is not new to Stage 34** — it has existed since Stage 15
introduced restarts, in both languages, unconditionally. It was never
observed before because the four fixed-conflict-count schedules'
bases are large (100 for Luby, 18000 for polynomial) — the exact
coincidence of "a restart threshold is crossed" landing on "the very
conflict that resolved to a level-0 unit clause" is astronomically
rare at those scales. `RestartGlucose` checks its own trigger
condition after *every single conflict* and can fire roughly every 50
conflicts, which made this reachable on real files
(`benchmark/blocksworld/bw_large.c.cnf` and `bw_large.d.cnf`) within
seconds.

**Fix**: `maybeRestart`/`maybe_restart` now returns immediately
whenever `currentLevel == 0`, before consulting any strategy —
there's nothing to restart from the root regardless of which schedule
is active, so this is a general fix, not a `RestartGlucose`-specific
one. `conflictsSinceRestart`/`restartCount` are left untouched (no
restart actually happened), so the next conflict re-evaluates from
the same state.

Regression tests were added in both languages
(`TestMaybeRestartDoesNotPanicAtRootLevel` /
`test_maybe_restart_does_not_panic_at_root_level`): construct a fresh
solver (`currentLevel == 0` by construction), force
`glucoseShouldRestart`/`glucose_should_restart` to report true by
feeding the ring buffer a low-then-high LBD sequence, call
`maybeRestart`, and assert it doesn't panic and leaves
`restartCount`/`conflictsSinceRestart` unchanged. Both
`bw_large.c.cnf` and `bw_large.d.cnf` were re-run after the fix, in
both languages, single- and multi-threaded (`--num-threads=4`); all
four now solve cleanly (`SATISFIABLE`, exit 0).

## Verification

- **Go**: `go build ./...`, `go vet ./...`, `gofmt -l .` all clean.
  `go test ./...` passes (all packages). `go test -race -count=2
  ./internal/cdcl/...` passes.
- **Rust**: `cargo build --release --all-targets`, `cargo fmt
  --check`, `cargo clippy --all-targets -- -D warnings` all clean.
  `cargo test --release` passes: 203 tests (198 before this stage,
  plus the 5 new ones described below).
- **New unit tests**, mirrored in both languages:
  - LBD correctness: `analyze`'s third return value on a
    hand-verified conflict (the same formula
    `TestAnalyzeDerivesUnitClauseIndependentOfDecision` already used)
    where both falsified literals sit at decision level 1 — asserts
    `lbd == 1`.
  - `reduceClauseDatabase`'s glue-clause protection: a clause at
    `glueClauseLBDThreshold` with the lowest possible activity (0.0)
    survives, while a lower-activity *non-glue* clause among the
    remaining eligible set is deleted instead — proving the exclusion
    actually changes the outcome versus pure activity sorting, not
    just that low-LBD clauses happen to survive anyway.
  - `reduceClauseDatabase`'s LBD-primary sort: a high-LBD,
    high-activity clause is deleted before a low-LBD, low-activity
    one — proving LBD dominates activity as the sort key, not the
    reverse.
  - `glucoseShouldRestart`/`glucose_should_restart`: doesn't trigger
    before the recent window is full; a fabricated low-then-high LBD
    sequence produces a restart signal matching a hand-computed
    `recentAvg*glucoseK >= globalAvg` comparison.
  - The root-level restart regression test described above.
  - `cliargs`: both languages' "accepts restart strategy 5" and
    "rejects out-of-range restart strategy" (now `6`, not `5`) tests.
- **Cross-language + real-file verification** via `util/benchcompare`
  (pre-built binaries, so runs reflect the actual `--alg-params 2 5`
  code path, not just unit-level state):
  - A 3-file smoke run confirmed the harness itself, then a 52-file
    sweep across `uf250-1065`, `uuf250-1065`, `blocksworld`, and
    `flat125-301` (`--sample=15` per directory, seed 42,
    `--time-limit-secs=20`) found the crash above.
  - After the fix, the identical 52-file sweep: **0 crashes, 0
    independent-verification failures, 0 cross-language verdict
    mismatches**, both languages solving 37/52 within the time limit
    (up from 36/52 immediately before the fix, since the two
    previously-crashing files now solve instead of erroring out).
  - `--num-threads=4` re-runs of both previously-crashing files, in
    both languages, all four clean.

## Benchmark results (`val2=2` vs. `val2=5`)

Same 52-file sample (`uf250-1065`, `uuf250-1065`, `blocksworld`,
`flat125-301`; `--sample=15`, seed 42, `--time-limit-secs=20`), same
two pre-built binaries, `val1=2` (VSIDS) fixed in both runs so only
the restart strategy differs:

| | solved (SAT/UNSAT) | mean (solved) | median (solved) |
|---|---|---|---|
| Go, `val2=2` (polynomial, current default) | 38/52 (33/5) | 2.608s | 0.047s |
| Go, `val2=5` (Glucose) | 37/52 (32/5) | 3.266s | 0.052s |
| Rust, `val2=2` (polynomial, current default) | 39/52 (33/6) | 2.812s | 0.029s |
| Rust, `val2=5` (Glucose) | 37/52 (32/5) | 2.929s | 0.031s |

**This is a negative result for `val2=5` as a default**, on this
project's own benchmark mix: it solved one to two *fewer* instances
within the same time budget than the existing polynomial schedule, in
both languages, with a higher mean solve time among the ones it did
solve. This doesn't mean the implementation is wrong — the crash it
exposed was in 15-stage-old code, not new logic, and the glue-clause
protection / LBD-primary sort in `reduceClauseDatabase` are unaffected
by this comparison (`val2=2` and `val2=5` both benefit from those,
identically) — it means Glucose's own restart cadence, tuned for its
authors' benchmark suite, isn't the better trigger schedule for this
project's particular random-3-SAT-heavy mix. This is the same
pattern this project already found for LRB-vs-VSIDS
(`reports/REPORT13.md`) and Luby-vs-polynomial
(`reports/REPORT15.md`): a real, credible technique from the
literature, correctly implemented, that this project's own
measurement says isn't the right default *here*.

**`val2=2` (polynomial) remains `cdcl`'s default.** `val2=5` is fully
available and correct for anyone who wants it (or for a future
`SelectVar`-style A/B benchmarking exercise, per `STAGE34.md`'s answer
to `REPORT33.md`'s question about a benchmark-driven default).

This is a single 52-file sample at one time limit, not an exhaustive
sweep — treat the specific numbers as directional, the way this
project's other benchmark-driven decisions have been reported, not as
a definitive final word on Glucose restarts for this codebase.

## Command line arguments

`--alg-params` (`--algorithm=cdcl` only), second value (restart
strategy), extended from `0-4` to `0-5`:

- `5` = Glucose's own data-driven policy: restart when a moving
  average of recent learned-clause LBDs looks close to or worse than
  the all-time average, rather than on a fixed conflict-count
  schedule.

`go_src/internal/cliargs/args.go`/`help.go` and
`rust_src/src/cliargs.rs` (both the parsing/validation and the
`--help` text) were updated together; `rust_src/src/main.rs`'s
`--alg-params`-to-`RestartStrategy` match gained a `Some([_, 5, ..]) =>
RestartStrategy::Glucose` arm. The default remains unchanged (`2`
single-threaded, `4` multi-threaded).

## Documentation

Per `STAGE34.md`'s question — "Have we been keeping
`docs/references.md` up to date when we add new features?" — the
answer, checked directly rather than assumed: **yes, through Stage
32.** Stages 25/28/29/30/31/32 were all performance-engineering, CI,
or benchmark-corpus work with no new literature-backed technique to
cite (Stage 26's Go lock-free deque rewrite was already covered by the
existing Chase & Lev citation, since that's the same algorithm the
Rust side already used via `crossbeam-deque`). `docs/references.md`
needed one addition for this stage's own new technique (Audemard &
Simon, IJCAI 2009 — added to both the "Complete search core algorithm"
and "Restarts" sections, since LBD is used by both clause-database
management and the new restart policy). `docs/usage.md`'s
`--alg-params` table and `docs/background.md`'s "Restart strategies"
section (plus a new "Clause-database management" section) are also
updated to describe `val2=5` and LBD-based deletion.

**Per `STAGE34.md`'s instruction, updating these three files is now a
standard, required part of every future stage** — checked and updated
(or explicitly confirmed already current, as most of Stages 25-32
were) before a stage is considered done, not just when a stage happens
to add a new citable technique.

## Answering `STAGE34.md`'s other items

Nothing else was in scope for this stage — `STAGE34.md`'s ranking
("clause management + the time-check fix + clause minimization" as
the next three stages, in that order) puts the time-based time-check
interval and learned-clause minimization next, not this stage. Also
explicitly deferred, per your own ranking: the `SelectVar` `F(a,b)`
benchmarking exercise (after clause min), the CDCL/local-search hybrid
(periodic rephasing), and the lookahead solver (back burner).

## Questions for you

- Given the negative benchmark result above, any interest in a
  follow-up that tunes Glucose's own parameters (`glucoseK`,
  `glucoseWindowSize`) against this project's benchmark set the way
  `REPORT15.md` tuned the Luby base — or is `val2=5` fine to leave as
  a correctly-implemented, non-default option and move on to the next
  stage?
- The root-level restart panic was a real, 15-stage-old latent bug
  that this stage's own testing happened to surface. Worth a quick
  note anywhere else (e.g. `docs/background.md`'s "Other things worth
  knowing"), or is the fix + regression test here sufficient?
- Ready to move to the time-based time-check interval fix next, per
  your own ranking?
