# Internal parameters

`vibe_sat`'s solving algorithms have always had a handful of constants
buried in the code that shape their behavior without being exposed on
the command line — a restart schedule's base interval, a decay rate,
a work-budget safety cap. `REPORT22.md`'s item 7 first asked for these
to be inventoried in one place; `STAGE39.md` (item 15) asks for that
inventory to actually be written, as groundwork for item 9's
`.vibe_sat.json` config system (see
[usage.md](usage.md#--internal-paramsfilename--c-filename) for how to
use it, and `--help`'s `--internal-params`/`--reset-internal-params`
entries for the command-line side).

This page lists every constant of this kind found in either
implementation — both the ones now exposed as tunable knobs via
`.vibe_sat.json`, and the ones deliberately left as compile-time
constants, with the reasoning for each classification. "Exposed"
means present in `params.Default()`/`Params::default()` and
overridable by a config file; everything else still requires editing
the source and rebuilding to change.

A parameter's JSON key (where it has one) is the name used inside
`.vibe_sat.json`; see [usage.md](usage.md) for the file's full shape.

## Exposed parameters (`.vibe_sat.json`)

### `cdcl` section

| JSON key | Default | Go / Rust location | What it controls |
|---|---|---|---|
| `lubyBaseConflicts` | `4000` | `cdcl.go` / `cdcl/mod.rs` | Scale factor for the Luby restart schedule (`RestartLuby`). Originally MiniSat's own default base (`100`); recalibrated by `STAGE49.md`'s benchmark sweep, which found the literature value measurably underperforms on this project's own benchmark set — see `REPORT49.md`. |
| `polynomialBaseConflicts` | `18000` | same | Scale factor for the polynomial restart schedule (`RestartPolynomial`). No literature standard exists for this one; chosen by this project's own benchmarking (see `REPORT18.md`/`REPORT19.md`). |
| `geometricBaseConflicts` | `100` | same | Base interval for the geometric restart schedule (`RestartGeometric`); previously tied to `lubyBaseConflicts` by definition, now independently tunable. |
| `geometricGrowthFactor` | `1.5` | same | Growth ratio ("c") applied to `geometricBaseConflicts` after every restart. |
| `lrbAlpha` | `0.1` | same | *Starting* exponential-moving-average learning-rate weight for the LRB (`SelectVarVariant::Lrb`) branching heuristic's Q-values (`STAGE49.md`: annealed once per conflict down to a floor thereafter — see the compile-time constants below). Recalibrated from `0.4` to `0.1` by `STAGE60.md`'s sweep: with `0.4`, 9–10 of 24 sampled files were unsolved in 20 s; `0.06`–`0.1` solved all of them and was the fastest — see `REPORT60.md`. |
| `clauseActivityDecay` | `0.999` | same | Per-conflict decay applied to the clause-activity increment (VSIDS-style clause bumping). |
| `varActivityDecay` | `0.95` | same | Per-conflict decay applied to the variable-activity increment (`SelectVarVsids`'s analogue of `clauseActivityDecay`). |
| `glueClauseLBDThreshold` | `2` | same | LBD at or below which a learned clause is a "glue clause," permanently exempt from `reduceClauseDatabase`'s deletion. |
| `glucoseWindowSize` | `50` | same | Size of the recent-LBD window `RestartGlucose` averages over (Glucose's own published value). Must be at least 1 (and at most 10,000,000, since the window is allocated up front). |
| `glucoseK` | `0.8` | same | Multiplier on the recent-window LBD average that `RestartGlucose` compares against the global average to decide whether to restart. `STAGE35.md` tuned it from Glucose's published `0.8` down to `0.6`; `STAGE60.md`'s wider sweep, with `0.8` and `0.9` and `1.0` now tested, found `0.8` about 15% faster than `0.6` on two independent file samples and `0.9`/`1.0` slower, so it is back at `0.8` — see `REPORT60.md`. |
| `minimizeWorkBudgetFactor` | `20` | same | Scales `minimizeClause`'s hard work-budget cap (`factor * n * (bits.Len(n)+1)`), bounding how much recursive-minimization work one learned clause can cost. |
| `rephaseIntervalRestarts` | `50` | `cdcl.go` / `cdcl/mod.rs` | How many restarts elapse between periodic WalkSAT rephasing bursts (`--alg-params` val4=2, `STAGE43.md`); meaningless for every other phase strategy. |
| `rephaseMaxFlips` | `1000` | same | Caps how many flips one WalkSAT rephasing burst may make, so the periodic "nudge" stays cheap relative to how rarely it fires. |

### `preprocess` section

| JSON key | Default | Go / Rust location | What it controls |
|---|---|---|---|
| `subsumptionWorkBudgetFactor` | `64` | `techniques.go` / `preprocess.rs` | Scales the work budget (`factor * len(clauses)`) that bounds subsumption elimination's cost per round. |
| `bveWorkBudgetFactor` | `2000` | same | Scales the work budget (`factor * len(clauses)`) that bounds bounded variable elimination's cost per round. Known to leave one pathological benchmark file (`apn-sbox5-cut3-symmbreak.cnf`, see `REPORT31.md`) short of BVE's full benefit; a candidate for `--internal-params` tuning (`STAGE39.md` item 25).

All 15 defaults above match the values these constants held before
`STAGE39.md`, except the ones later recalibrated by a benchmark sweep
(`lubyBaseConflicts`, `lrbAlpha`, `glucoseK`; see their rows); enabling runtime configuration did not change
any existing default behavior in either language.

### Valid ranges

`vibe_sat` validates every parameter when the config file is loaded
(`STAGE56.md`, `STAGE58.md`) and stops with an error naming the first
offending field, e.g. `cdcl.varActivityDecay must be greater than 0 and
at most 1 (got 2)`. The ranges are identical in Go and Rust and are also
listed in `--help`. A value of the wrong JSON type is an error too (the
exact wording of that message differs between the two languages).

| JSON key | Valid range | Why |
|---|---|---|
| `cdcl.lubyBaseConflicts` | integer, 1 to 1,000,000,000 | Positive scale factor; the upper cap keeps `base * Luby term` far below 2^63 (see the cap note below). |
| `cdcl.polynomialBaseConflicts` | integer, 1 to 1,000,000,000 | Same; multiplied by the square of the restart count. |
| `cdcl.geometricBaseConflicts` | integer, at least 1 | Base interval of the geometric schedule. The threshold computation saturates at INT64_MAX (`STAGE56.md`), so no upper cap is needed. |
| `cdcl.geometricGrowthFactor` | finite number, at least 1 | A ratio below 1 would shrink the restart interval toward zero. |
| `cdcl.lrbAlpha` | number, greater than 0 and at most 1 | An exponential-moving-average weight. |
| `cdcl.clauseActivityDecay` | number, greater than 0 and at most 1 | A decay of 0 or above 1 makes activities collapse or shrink; `varActivityDecay: 2.0` stopped uf250-01 from reaching a verdict in 3 s. |
| `cdcl.varActivityDecay` | number, greater than 0 and at most 1 | Same. |
| `cdcl.glueClauseLBDThreshold` | integer, at least 0 | 0 exempts no clause. |
| `cdcl.glucoseWindowSize` | integer, 1 to 10,000,000 | The LBD window is allocated up front: 0 or a negative size crashed, and an enormous one runs out of memory. |
| `cdcl.glucoseK` | finite number, greater than 0 | |
| `cdcl.minimizeWorkBudgetFactor` | integer, 0 to 1,000,000,000 | 0 leaves minimization no work budget. |
| `cdcl.rephaseIntervalRestarts` | integer, at least 1 | |
| `cdcl.rephaseMaxFlips` | integer, at least 1 | |
| `preprocess.subsumptionWorkBudgetFactor` | integer, 0 to 1,000,000,000 | Multiplied by the clause count. |
| `preprocess.bveWorkBudgetFactor` | integer, 0 to 1,000,000,000 | Multiplied by the clause count. |

**The 1,000,000,000 cap** (`MaxScaleFactor` in `go_src/internal/params`,
`MAX_SCALE_FACTOR` in `rust_src/src/params.rs`) applies to the five
integer parameters that are multiplied by something large. It is over
four orders of magnitude above the largest default (18,000), so it can
stand in for "unlimited", and it keeps every product far below 2^63.
Without it, a value like INT64_MAX silently wrapped in Go and switched
bounded variable elimination off on formulas with an even clause count
(`REPORT58.md`). In Rust the INT64_MAX literal is rejected with a
different message (`must be an integer`), because its JSON reader
holds numbers as f64.

NaN and infinities are rejected for every float field. Only the first
violation is reported per load.

### Preprocessing and `--time-limit-secs`

Preprocessing time does **not** count against `--time-limit-secs`
(`STAGE60.md`). When a limit is given, preprocessing is allowed at most
one tenth of it and then stops, leaving a less-simplified but still
equivalent formula; the search then gets the full limit. Total wall time
can therefore reach 1.1 times the limit. With no limit, preprocessing
runs to completion. The one tenth is the constant `TimeFraction`
(`preprocess.Deadline` in Go, `preprocess::deadline` in Rust), not a
`.vibe_sat.json` parameter.

## Constants considered but not exposed

These are also project-defined "magic numbers" a reader might expect
on the list above, but each is a structural constant rather than a
search-quality knob — changing it doesn't trade off solving
effectiveness the way the parameters above do, so a config file has
no real use for it.

| Name | Value | Location | Why not exposed |
|---|---|---|---|
| `activityRescaleThreshold` | `1e100` | `cdcl.go` / `cdcl/mod.rs` (`ACTIVITY_RESCALE_THRESHOLD`) | A floating-point overflow safety valve, not a tuning parameter — any value large enough to almost never bind works identically; there is nothing to trade off by changing it. |
| `lrbAlphaDecayStep` | `1e-6` | `cdcl.go` / `cdcl/mod.rs` (`LRB_ALPHA_DECAY_STEP`) | `STAGE49.md`'s per-conflict decay applied to `lrbAlpha`'s annealed running value (`lrbCurrentAlpha`/`lrb_current_alpha`). Not benchmark-motivated to tune independently yet — see `REPORT49.md`. |
| `lrbAlphaFloor` | `0.06` | `cdcl.go` / `cdcl/mod.rs` (`LRB_ALPHA_FLOOR`) | The floor `lrbAlphaDecayStep`'s annealing clamps at, per the LRB paper's own schedule. Same rationale as `lrbAlphaDecayStep` for not being exposed. |
| `exportMaxClauseLen` | `8` | `clauseshare.go` / `cdcl/mod.rs` | Reuses ManySAT's own literature convention for which learned clauses are worth sharing between threads; not benchmark-tuned by this project, and STAGE20/21's clause-sharing design treats it as a fixed policy choice rather than a dial. |
| `exportBufferCapacity` | `256` | `clauseshare.go` / `cdcl/mod.rs` | A "generous, cheap-to-hold guess" per its own doc comment — sizing a lossy ring buffer where correctness never depends on the value, only how often a peer might miss a shared clause. Not measured to matter. |
| `importCheckMask` | `0x1f` (check every 32 conflicts) | `clauseshare.go` / `cdcl/mod.rs` | A pure amortization choice (how often to poll peers for shared clauses), not a search-quality parameter — no known instance is sensitive to this value. |
| `maxRounds` | `1000` | `preprocess.go` / `preprocess.rs` | A safety net against a hypothetical non-converging preprocessing loop; the pipeline converges in far fewer rounds in practice (each round strictly shrinks the formula), so this never actually binds. |
| `dequeInitialCapacity` | `32` | `dfs/deque.go` (Rust `dfs` module equivalent) | A backing-array size hint for `dfs`'s work-stealing deque; must be a power of two by construction, and only affects how soon the deque's first grow-and-copy happens, not its steady-state behavior. |
| `shedCheckInterval` | `0xfffff` | `dfs/parallel.go` (Rust equivalent) | Deliberately chosen, per `REPORT32.md`'s investigation, to make load-shedding checks rare enough to avoid a measured regression; changing it trades rebalancing frequency for overhead in a way `REPORT32.md` already explored directly and settled — a candidate for revisiting only alongside that report's own follow-up, not a general-purpose knob. |
| `DefaultMaxFlipsPerTry` | `10000` | `hillclimb/walksat.go` (Rust equivalent) | Already runtime-configurable — via `--alg-params` for the `hc`/`ws` algorithms, not `.vibe_sat.json` — since Stage 17; it's a per-run search parameter with its own existing CLI path, not a hidden constant. |
| `DefaultNoisePercent` | `50` | same | Same reasoning as `DefaultMaxFlipsPerTry` — already exposed via `--alg-params`. |

`noReason` (`cdcl.go`, `-1`) and the various `iota`-based enum
constants across `dfs.go`/`assign.go`/`parallel.go` are omitted
entirely: they're internal sentinel/tag values (a trail-index "no
reason" marker, `Status`/`SelectVarVariant`/`frameNext` discriminants,
etc.), not numeric parameters with a "better" or "worse" value —
there is nothing for a config file to tune.

## Design notes (`STAGE39.md`'s open questions)

**Implicit vs. explicit config.** Both, together, not a choice
between them: an explicit `--internal-params=<path>` always wins when
given; otherwise `.vibe_sat.json` in the current directory is used
automatically if present; otherwise every parameter keeps its
built-in default. An explicit path that can't be read or parsed is a
hard error (the run stops) rather than a silent fallback to
defaults — if you asked for a specific file, getting your own typo's
defaults back instead would be worse than an error.

**Tuning one parameter at a time, or several jointly?** One at a
time (coordinate descent), by default, matching the precedent already
set manually in `REPORT35.md`'s `glucoseK` sweep — sweeping every
combination of even a handful of continuous-valued parameters is
combinatorially expensive against real benchmark instances, and this
project's existing tuning work has never found evidence that these
parameters interact strongly enough to need joint search. See
`REPORT39.md` for how `util/paramtune` implements this and where a
small joint grid is still supported for parameters suspected to
interact.
