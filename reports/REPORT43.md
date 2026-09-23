# Report: Stage 43

## Summary

`STAGE43.md` asked for `REPORT41.md`'s #2 tweak: try phase-selection
techniques `vibe_sat` doesn't have yet (target phases, periodic
WalkSAT rephasing), and raised two design questions — should the
method be user-selectable or should the solver try them all and pick
the best automatically, and what happens if a WalkSAT rephasing burst
solves the problem outright.

Done in both languages:

- **Two new phase-selection strategies**, alongside the existing
  phase saving (Stage 14): **target phase** (Chanseok Oh) and
  **periodic WalkSAT rephasing** (`REPORT33.md` item 6), selectable
  via a new fourth `--alg-params` value.
- **The WalkSAT-solves-it edge case is handled correctly**: a
  rephasing burst that happens to return a complete satisfying
  assignment is reported as the search's own verdict immediately,
  never discarded.
- **Design question answered**: selectable via `--alg-params`
  (matching `SelectVarVariant`/`RestartStrategy`'s existing
  convention), *and* diversified across worker threads in
  multi-threaded mode (matching `RestartRoundRobin`'s existing
  convention) — not a new "try-them-all" mechanism, since the
  portfolio's existing "whichever worker finishes first wins" design
  already gets that property for free.
- **Benchmarked against the default (plain phase saving) on the
  250-variable random set** (the same instances `REPORT40.md`/
  `REPORT42.md` focused on): neither new technique beat the existing
  default at their initial parameter settings. **No default changed.**
  Both remain available as real, correctly-implemented options, joining
  `RestartGlucose` in this project's "measured, not adopted as
  default (yet)" category.

## Design: user-selectable, plus portfolio diversification

Both are true, not one or the other. `--alg-params`'s new fourth value
(`val4`, cdcl only) selects a phase strategy explicitly:
`0`=saving (default), `1`=target, `2`=WalkSAT rephasing,
`3`=round-robin. This mirrors exactly how `val1` (SelectVar) and
`val2` (restart strategy) already work — a new, separate configuration
mechanism for the same kind of choice would only raise the question of
which one wins.

For the "try them all" half of the question: with `--num-threads > 1`
and no explicit `val4`, the default becomes round-robin — worker 0
gets phase saving, worker 1 target phase, worker 2 WalkSAT rephasing,
worker 3 back to phase saving, and so on — exactly the precedent
`RestartRoundRobin` already set for restart schedules. This needs no
new decision logic: `RunParallel`'s existing "first worker to reach a
verdict wins" design already means whichever strategy actually helps
on a *given* instance is the one whose worker is most likely to win
the race, without ever needing to run the search multiple times just
to compare strategies against each other. This was preferred over
building a genuine sequential try-N-then-pick-the-best mechanism
within a single-threaded run, which would need to run the whole search
several times just to know which strategy to trust.

**One honest caveat**: `RestartRoundRobin`'s three schedules
(polynomial/geometric/Luby) were all independently reasonable before
being combined into a rotation — none measured as clearly broken on
its own. This stage's own benchmark (below) found `PhaseTarget`
measurably *worse* than the default standalone, not just different —
unlike restart's rotation, phase round-robin's diversity argument
hasn't been independently re-verified in the multi-threaded setting
specifically; it's carried over by analogy to restart's precedent, not
its own dedicated measurement. Flagged here rather than glossed over.

## The WalkSAT-solves-it edge case

`STAGE43.md` flagged this explicitly: WalkSAT is a complete SAT-solving
method on its own, not just a phase-quality heuristic, so a periodic
rephasing burst can — rarely, but really — return a fully satisfying
assignment by itself. Both languages check for this immediately: if
the burst's result is satisfiable, that assignment is reported as the
search's own verdict directly (Go: `solver.earlyExitSAT`/
`earlyExitAssignment`, checked once per main-loop iteration right
after `maybeRestart`; Rust: `maybe_restart` returns
`Option<Assignment>`, an idiomatic alternative to an out-parameter
given this file's existing "explicit locals threaded through
`run_loop`" style rather than Go's method-receiver style) — never
silently discarded in favor of continuing to search for a solution via
CDCL that's already in hand. Verified directly: a dedicated test in
both languages runs a real WalkSAT burst against a trivial single-
clause problem and confirms the early-exit path fires (Go:
`TestRephaseFromWalkSATSolvesEarlyExit`/`TestRunLoopReturnsSATOnWalkSATEarlyExit`
in `internal/cdcl/phase_test.go`; Rust mirrors both).

## What was implemented

- **Target phase** (`val4=1`): a second, separate array
  (`targetPhase`/`target_phase`) tracks the polarity every variable
  held at the search's single deepest trail point so far (the most
  variables ever simultaneously assigned without conflict — an
  approximation for "the assignment that came closest to satisfying
  everything"), updated once per main-loop iteration whenever the
  trail sets a new record. Never overwritten by ordinary phase-saving
  updates, and ties keep the earlier snapshot. This is a deliberately
  simplified reading of the technique: real implementations (CaDiCaL,
  Kissat) cycle between several phase sources across a stable/unstable
  search-mode switch; this implementation only tracks and uses the one
  target array, no mode-cycling.
- **Periodic WalkSAT rephasing** (`val4=2`): every
  `params.CDCL.RephaseIntervalRestarts` restarts (new internal
  parameter, default 50), a short WalkSAT burst
  (`internal/hillclimb.RunWalkSat`/Rust's equivalent, bounded to
  `params.CDCL.RephaseMaxFlips` flips — new internal parameter,
  default 1000) runs over the *current* clause database (reusing the
  solver's own live occurrence lists, already extended with every
  learned clause so far — not a fresh rebuild from the original
  problem only, so the burst benefits from everything CDCL has learned
  too) and its resulting assignment overwrites the saved-phase array
  wholesale.
- **A fourth family from the same literature, documented but not
  implemented**: Kissat's "inverted" (flip every saved-phase bit) and
  "original" (reset to the fixed pre-search phase) rephase targets,
  used purely for diversification rather than because either polarity
  is expected to be better on its own. Left out because building the
  scheduling/mode-cycling architecture real solvers wrap around this
  full family is a bigger change than this stage's scope, not because
  the idea itself is somehow less real than the two implemented here —
  see the package doc comment for the full citation.

Two new internal parameters (`rephaseIntervalRestarts`,
`rephaseMaxFlips`), documented in `docs/internal-parameters.md`,
join the existing thirteen — exposed via `.vibe_sat.json` like every
other Stage 39 tuning constant, with the same "not yet independently
tuned, just given a reasonable starting default" status
`bveWorkBudgetFactor` had before its own Stage 42 sweep.

## Benchmark: does either new technique beat the default?

Direct, single-threaded, Go-only comparison (30 files: 15 from
`benchmark/uf250-1065`, 15 from `benchmark/uuf250-1065` — the same
family `REPORT40.md`/`REPORT42.md` already focused on), `--alg-params
2 2 1GB <phase>`, `timeout 20` per file, params otherwise at their
just-added defaults:

| Phase strategy | Unsolved (of 30) | Total elapsed |
|---|---:|---:|
| `0` — phase saving (default) | 11 | 353.3s |
| `1` — target phase | **14** | 356.6s |
| `2` — WalkSAT rephasing | 11 | 359.6s |

**Target phase is clearly worse** — 3 more unsolved files than the
default, not just a time difference. **WalkSAT rephasing ties on
completion but costs slightly more time** — an effective wash, not an
improvement, at these particular interval/flip-budget defaults.
**No default changed**: phase saving remains `cdcl`'s single-threaded
default, exactly as measurement — not literature reputation — already
decided VSIDS over LRB (`REPORT13.md`) and polynomial over Luby
(`REPORT15.md`).

This doesn't mean either technique is worthless: `RephaseIntervalRestarts`/
`RephaseMaxFlips` were given reasonable starting defaults, not
benchmark-tuned ones — a `util/paramtune` sweep over those two
parameters specifically (the same kind of follow-up `bveWorkBudgetFactor`
got in Stage 42) is a real, cheap next step if periodic rephasing is
worth a second look before writing it off. Target phase's simplified,
no-mode-cycling implementation may also simply need the fuller
Kissat-style architecture to pay off — a single fixed array, used for
the whole run, may not be enough on its own.

## Verification

- **Go**: `go build ./...`, `go vet ./...`, `gofmt -l .` clean;
  `go test ./...` (all ten packages) and `go test -race -count=2
  ./internal/cdcl/...` clean. Ten new tests in a new
  `internal/cdcl/phase_test.go`, covering `resolvePhaseStrategy`'s
  round-robin resolution, `phaseFor`'s per-strategy array selection,
  `updateTargetPhase`'s record-tracking and tie-handling,
  `maybeRephase`'s strategy/interval gating (including a malformed
  non-positive-interval guard), and both a direct and an end-to-end
  test of the WalkSAT-solves-it early exit.
- **Rust**: `cargo build --release`, `cargo build --all-targets`,
  `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`
  clean; `cargo test --release` — 246 passed, 0 failed (11 new tests
  mirroring the Go suite exactly).
- **Cross-language check, done directly**: both binaries, all four
  `val4` values, against both a SAT and an UNSAT instance —
  byte-identical verdicts and verbose `cdcl: ... phase=N` announcement
  lines in every case. `--num-threads=4` with no explicit `--alg-params`
  produces `phase=3` (round-robin) in both languages.
- **One real inconsistency caught and fixed during this verification**:
  the Rust port (built by a fork before a Go-side range-validation
  addition landed) initially accepted an out-of-range `val4` (e.g. `5`)
  silently, falling back to phase saving, while Go correctly rejected
  it — an ordering mistake on my part (the Go validation was added
  after the Rust port was already dispatched), not a fork error. Fixed
  directly: both languages now reject `val4` outside `0`-`3` with a
  matching error message and have a test confirming it.

## Documentation

- `docs/usage.md` — `--alg-params`'s `cdcl` table extended with `val4`.
- `docs/internal-parameters.md` — the two new `rephaseIntervalRestarts`/
  `rephaseMaxFlips` parameters added to the `cdcl` table.
- `docs/background.md` — new "Phase-selection strategies" section,
  parallel in structure and tone to the existing "Restart strategies"
  section right above it.
- `docs/references.md` — not updated: Chanseok Oh's target-phase work
  and the WalkSAT-rephasing idea (`REPORT33.md`) don't have a single
  canonical paper citation the way Glucose/LRB/Luby do (target phase
  in particular is more of a lineage — MapleSAT, CaDiCaL, Kissat — than
  one paper); noted here rather than forcing an artificial citation.

## Questions for you

- Worth a `util/paramtune` sweep over `rephaseIntervalRestarts`/
  `rephaseMaxFlips` specifically (mirroring Stage 42's
  `bveWorkBudgetFactor` follow-up), to check whether WalkSAT rephasing
  can be made to actually help at a different interval/budget, before
  writing it off entirely? It was a near-tie at these first-guess
  defaults, not a clear loss the way target phase was.
- Given target phase's simplified (no mode-cycling) implementation
  measured clearly worse standalone, is it worth a future stage
  building the fuller Kissat-style rotation this literature actually
  describes (cycling between saved/target/inverted/original/random
  phases across stabilizing-mode restarts), or does that cross into
  "bigger algorithmic change" territory you'd rather scope separately
  (`REPORT41.md`'s tier 3), given this stage's own measurement didn't
  find the simplified version worth keeping on its own?
- The multi-threaded `PhaseRoundRobin` default carries over restart's
  diversification precedent by analogy, not independent multi-threaded
  measurement — worth a dedicated benchmark of multi-threaded `cdcl`
  with vs. without phase diversification before trusting it, or is the
  architectural argument (diversity helps the shared clause pool)
  convincing enough on its own?
