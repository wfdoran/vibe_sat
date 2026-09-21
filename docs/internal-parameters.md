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
| `lubyBaseConflicts` | `100` | `cdcl.go` / `cdcl/mod.rs` | Scale factor for the Luby restart schedule (`RestartLuby`) — MiniSat's own default base. |
| `polynomialBaseConflicts` | `18000` | same | Scale factor for the polynomial restart schedule (`RestartPolynomial`). No literature standard exists for this one; chosen by this project's own benchmarking (see `REPORT18.md`/`REPORT19.md`). |
| `geometricBaseConflicts` | `100` | same | Base interval for the geometric restart schedule (`RestartGeometric`); previously tied to `lubyBaseConflicts` by definition, now independently tunable. |
| `geometricGrowthFactor` | `1.5` | same | Growth ratio ("c") applied to `geometricBaseConflicts` after every restart. |
| `lrbAlpha` | `0.4` | same | Exponential-moving-average learning-rate weight for the LRB (`SelectVarVariant::Lrb`) branching heuristic's Q-values. |
| `clauseActivityDecay` | `0.999` | same | Per-conflict decay applied to the clause-activity increment (VSIDS-style clause bumping). |
| `varActivityDecay` | `0.95` | same | Per-conflict decay applied to the variable-activity increment (`SelectVarVsids`'s analogue of `clauseActivityDecay`). |
| `glueClauseLBDThreshold` | `2` | same | LBD at or below which a learned clause is a "glue clause," permanently exempt from `reduceClauseDatabase`'s deletion. |
| `glucoseWindowSize` | `50` | same | Size of the recent-LBD window `RestartGlucose` averages over (Glucose's own published value). |
| `glucoseK` | `0.6` | same | Multiplier on the recent-window LBD average that `RestartGlucose` compares against the global average to decide whether to restart. Benchmark-tuned away from Glucose's own published `0.8` — see `REPORT35.md`. |
| `minimizeWorkBudgetFactor` | `20` | same | Scales `minimizeClause`'s hard work-budget cap (`factor * n * (bits.Len(n)+1)`), bounding how much recursive-minimization work one learned clause can cost. |

### `preprocess` section

| JSON key | Default | Go / Rust location | What it controls |
|---|---|---|---|
| `subsumptionWorkBudgetFactor` | `64` | `techniques.go` / `preprocess.rs` | Scales the work budget (`factor * len(clauses)`) that bounds subsumption elimination's cost per round. |
| `bveWorkBudgetFactor` | `2000` | same | Scales the work budget (`factor * len(clauses)`) that bounds bounded variable elimination's cost per round. Known to leave one pathological benchmark file (`apn-sbox5-cut3-symmbreak.cnf`, see `REPORT31.md`) short of BVE's full benefit; a candidate for `--internal-params` tuning (`STAGE39.md` item 25).

All 13 defaults above match the values these constants held before
`STAGE39.md`; enabling runtime configuration did not change any
existing default behavior in either language.

## Constants considered but not exposed

These are also project-defined "magic numbers" a reader might expect
on the list above, but each is a structural constant rather than a
search-quality knob — changing it doesn't trade off solving
effectiveness the way the parameters above do, so a config file has
no real use for it.

| Name | Value | Location | Why not exposed |
|---|---|---|---|
| `activityRescaleThreshold` | `1e100` | `cdcl.go` / `cdcl/mod.rs` (`ACTIVITY_RESCALE_THRESHOLD`) | A floating-point overflow safety valve, not a tuning parameter — any value large enough to almost never bind works identically; there is nothing to trade off by changing it. |
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
