# Report: Stage 52

## Summary

`STAGE52.md` asked for `REPORT50.md`'s Tier 1 item 2: bootstrap
interruptibility, closing `REPORT35.md`'s own disclosed limitation —
`UnitPropagate`, the initial watch-selection loop, and `occurrence.Build`
had no time-limit awareness at all, so a single bootstrap call slower
than `--time-limit-secs` ran to completion regardless, with the search
loop never getting a chance to check anything until it finished on its
own.

**Result: fixed in both languages, confirmed on the exact real-world
scenario `REPORT29.md`/`REPORT35.md` originally diagnosed.** All three
steps now check the deadline themselves (`UnitPropagate` down to its
own per-clause loop, so a timeout is noticed mid-scan, not just between
whole propagation rounds), and report a genuine three-way outcome —
*ok*, *unsat*, or *timed out* — rather than the previous two-way
"ok or not," which had no way to represent "don't know." On the same
1.5M-variable, 6.3M-clause industrial instance, `--no-preprocessing
--time-limit-secs=3`: Go's overrun dropped from ~11.8s to ~4.2s for
both `cdcl` and `dfs`; Rust's dropped from ~15.5s to ~3.8s. Also
handled: a small housekeeping addendum to `REPORT40.md` (adding
`uf250-03.cnf`, which regressed under `STAGE51.md`'s fix, to the
standing `minisat`/`cryptominisat5` comparison file list for next time).

## The fix

Every affected function gained a `timeLimit`/`startTime` pair (Go:
`*time.Duration`/`time.Time`, matching `dfs.Run`'s/`cdcl.runLoop`'s own
existing convention exactly; Rust: `Option<Duration>`/`Instant`, same
idiom) and a new `timedOut` signal, checked unconditionally — no
periodic-interval gating, matching `STAGE35.md`'s own established
"a raw clock check costs ~14ns, unmeasurable next to real per-clause
work" finding:

- **`simplifyWithAssignment`**: checked once per clause, before that
  clause's own work begins. On timeout, the clause set is left exactly
  as it was before the call in Go (the in-place compaction write only
  happens on the normal return path) — Rust's owned-`Vec` version can't
  cheaply preserve that same property mid-scan once it has taken
  ownership of the input (`std::mem::take`), so a Rust mid-scan timeout
  leaves `clauses` empty rather than restored; this is safe and
  explicitly documented, since every real caller discards the clause
  set entirely the moment `timed_out` comes back true regardless of
  what state it's left in.
- **`UnitPropagate`**: threads the above through, and separately checks
  the deadline once per clause in its own unit-clause-finding loop.
- **The initial watch-selection loop** (`newSolver`'s own loop in Go;
  `new_watch_state` in both `dfs` and, via `chooseWatch`, `cdcl`):
  checked once per clause.
- **`occurrence.Build`**: a new `BuildWithDeadline` (Go)/
  `build_with_deadline` (Rust) does the same per-clause check; the
  existing `Build`/`build` stays exactly as it was, as a thin wrapper
  with no deadline, since every other caller (`hillclimb`, `ws`, `cdcl`'s
  own `reduceClauseDatabase`, and every test) doesn't need one and
  shouldn't have its behavior touched.

**The three-way outcome distinction is the part that actually matters
for correctness**, not just responsiveness: `newSolver`/`bootstrap` (Go
and Rust, `cdcl` and `dfs`) now report *ok*, *unsat*, or *timedOut*
separately, and every call site checks `timedOut` before checking
`ok`/`unsat` — a timed-out bootstrap must produce `Result{TimedOut:
true}`, never `Result{Satisfiable: false}`, since the latter would
falsely claim a proof of unsatisfiability for a problem whose real
status is unknown. This is directly, deliberately unit-tested (see
below): every new "distinguish timeout from unsat" test uses a genuine
two-unit-clause contradiction specifically so the test fails loudly
with a wrong verdict if the timeout check is ever skipped or checked
too late, not just silently slower.

## A related, disclosed-but-unfixed gap

`preprocess.Run` (Stage 8's own subsumption/BVE/pure-literal pipeline,
called from `cmd/vibe_sat/main.go`) runs entirely outside
`--time-limit-secs`'s scope too — before either algorithm's own
`startTime` is even captured, so its cost was never counted against
the budget at all, let alone interruptible. This is a real gap, and
arguably a bigger one than the bootstrap path this stage fixed, but
it's a substantially larger change (subsumption and BVE would each
need their own deadline threading, layered on top of the work-budget
safety caps they already have for an unrelated reason). `STAGE52.md`'s
own scope, following `REPORT35.md`'s "known, disclosed limitation"
wording exactly, was the narrower bootstrap path each algorithm runs
on its own — every call site inside `preprocess.Run`'s pipeline
(including its own internal `UnitPropagate`/`simplifyWithAssignment`
calls) explicitly passes "no limit," preserving its exact previous,
unconditional behavior. Recorded here, and in `docs/background.md`, so
a future stage doesn't have to rediscover it.

## Measured results

**Real end-to-end verification** on the exact file `REPORT29.md`/
`REPORT35.md` diagnosed
(`sat_comp/2018/e16c33062a8fa678573cd7b335187b32-Problem11_label29_false-unreach-call.c.cnf`,
1,497,096 variables, 6,358,575 clauses), `--no-preprocessing
--time-limit-secs=3`, wall-clock as printed by the CLI itself (so this
includes CNF parsing and problem setup, which happen before either
algorithm's own time-limit clock starts and are unaffected by this
fix):

| | Go, before → after | Rust, before → after |
|---|---:|---:|
| `--algorithm=cdcl` | 11.87s → **4.22s** | 15.49s → **3.79s** |
| `--algorithm=dfs` | 11.72s → **4.31s** | 15.43s → **3.83s** |

Both languages: still UNSAT/UNKNOWN as before (a timeout, correctly
reported, not a wrong verdict), just far closer to the requested
3-second budget. The residual ~1-1.3s over budget in both languages is
CNF parsing/setup cost, outside this fix's scope (and outside
`--time-limit-secs`'s own scope entirely, by design — the flag bounds
the *algorithm's* time, not process startup).

**One honest methodological note**: Go's "before" binary here predates
`STAGE51.md`'s `chooseWatch` fix too (both stages' Go changes were
uncommitted together, and isolating a clean post-51-pre-52-only Go
binary wasn't done for this specific measurement), while Rust's
"before" binary is specifically post-51-pre-52 (a binary saved during
`STAGE51.md`'s own verification was still on disk). This doesn't
change the conclusion — this scenario is bootstrap-dominated, not
search-loop-dominated (bootstrap alone eats the entire 3-second budget
several times over, so the actual CDCL/DFS search loop where
`chooseWatch` matters barely runs at all here) — but it means Go's and
Rust's specific before/after percentages aren't a perfectly
apples-to-apples pair with each other, even though each is internally
a valid, real before/after comparison in its own right.

## Verification

- **Go**: `go build ./...`, `go vet ./...`, `gofmt -l .` clean; `go
  test ./...` (all ten packages) and `go test -race -count=10
  ./internal/cdcl/... ./internal/dfs/... ./internal/preprocess/...
  ./internal/occurrence/...` clean.
- **New Go tests**: `TestUnitPropagateReportsTimedOutBeforeFindingUnitClause`,
  `TestSimplifyWithAssignmentReportsTimedOutWithoutMutatingClauses`
  (`internal/preprocess`); `TestBuildWithDeadlineReportsTimedOutBeforeFirstClause`
  (`internal/occurrence`); `TestNewWatchStateReportsTimedOutBeforeSecondClause`,
  `TestBootstrapReportsTimedOutNotUnsat` (`internal/dfs`);
  `TestNewSolverReportsTimedOutNotUnsat`,
  `TestRunLoopReportsTimedOutWhenBootstrapTimesOut` (`internal/cdcl`).
  Every one broken (the specific deadline check it verifies temporarily
  disabled), confirmed to fail with the expected wrong-verdict message,
  then restored and confirmed passing.
- **Rust**: `cargo build --release`/`--all-targets`, `cargo fmt
  --check`, `cargo clippy --all-targets -- -D warnings` clean; `cargo
  test --release`: **273 passed, 0 failed** (was 266, after `STAGE51.md`'s
  own work; 7 new tests, one per Go test above, same names in
  `snake_case`). The Rust port was built by a forked subagent that hit
  this session's own usage limit mid-task (interrupted while restoring
  a deliberate teeth-check break in `dfs/mod.rs`) before delivering its
  own final report; I independently re-verified everything myself
  afterward rather than trusting an unconfirmed claim: full build/fmt/
  clippy/test suite re-run from scratch (clean), the complete diff read
  end-to-end file by file, and I personally re-did the
  timeout-vs-unsat teeth-check on `cdcl::run_loop` (broke it, confirmed
  the expected test failure, restored, confirmed passing again) and the
  real end-to-end big-file timing measurements above myself, rather
  than relying on the interrupted fork having actually done them.
- `choose_watch`'s Rust return shape (`Option<(Literal, usize)>`, from
  `STAGE51.md`) composes cleanly with this stage's own consistent
  `(Option<T>, bool)` shape for every changed function
  (`new_watch_state`, `bootstrap` in both `dfs`/`cdcl`,
  `build_with_deadline`) — confirmed by reading `cdcl::bootstrap`'s own
  call to `choose_watch(clause, &x, None, 0)`, correctly discarding the
  scan-position return value it has no use for at construction time,
  exactly mirroring Go's own `newSolver`.

## Documentation

- `docs/background.md` — new "Time limits and bootstrap
  interruptibility" section (placed after "Preprocessing," before
  "Restart strategies," since the topic spans both algorithms and sits
  between preprocessing and search): the `REPORT29.md`/`REPORT35.md`
  history, what `STAGE52.md` fixed, the measured before/after numbers,
  and the disclosed `preprocess.Run` gap above.
- `reports/REPORT40.md` — addendum recording `uf250-03.cnf`'s addition
  to the standing 25-file comparison set, per your instruction from
  `STAGE51.md`'s questions.
- `docs/usage.md`/`docs/internal-parameters.md`/`docs/references.md` —
  no changes: `--time-limit-secs`'s own description was already
  accurate (it never claimed periodic-only checking), no new CLI flag
  or tunable parameter was added, no new literature citation (this is
  the project's own engineering fix, not a published technique).

## Questions for you

- The disclosed `preprocess.Run` gap (Stage 8's own pipeline running
  entirely outside `--time-limit-secs`) is arguably bigger than what
  this stage fixed, but also a bigger lift (subsumption/BVE would each
  need their own deadline threading). Worth its own future stage, or
  is "the search's own defensive re-propagation is now interruptible"
  enough for now, matching how `REPORT35.md` itself left this exact
  question open before?
- Go's and Rust's "before" baselines for the big-file measurement
  weren't perfectly matched (see the methodological note above) — I
  judged this didn't matter given the scenario is bootstrap-dominated,
  not search-loop-dominated, but flagging the reasoning rather than
  silently presenting the numbers as a clean controlled pair.
- Per `REPORT50.md`'s Tier 1 ordering, the progress meter /
  heartbeat is next. Still the right call, or does anything here change
  your sense of priority?
