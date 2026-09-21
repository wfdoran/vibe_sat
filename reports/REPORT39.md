# Report: Stage 39

## Summary

`STAGE39.md` asked for three things: an inventory of every internal
tuning constant (item 15), a runtime-configuration system plus
auto-tuning harness built on top of it (item 9), and — hoped to
follow "for free" from those two — an attempt to tune
`bveWorkBudgetFactor` against the one pathological file `REPORT31.md`
flagged (item 25).

All three are done:

- **`.vibe_sat.json`**: a new JSON config file, identical schema in
  both languages, exposing the 13 tuning constants both languages
  already had as compile-time values. Both `--internal-params=<path>`
  (explicit) and automatic `.vibe_sat.json` discovery (implicit) are
  supported together — see "Design questions" below for why not one
  or the other. `--reset-internal-params`/`-q` writes a starter file
  populated with defaults. No existing default behavior changed in
  either language.
- **`docs/internal-parameters.md`**: the full inventory — the 13
  exposed parameters and, just as importantly, every constant that
  looks like a tuning knob but isn't (with the reasoning for each).
- **`util/paramtune`**: a new standing tool, sibling to
  `util/benchcompare`, that sweeps candidate values for one or more
  `.vibe_sat.json` parameters and reports which value performs best,
  by coordinate descent by default (one parameter/group at a time).
- **`bveWorkBudgetFactor` tuning**: measured directly (see below).
  **No change to the default (2000).** Raising the budget eliminates
  more variables but costs proportionally more preprocessing time,
  and — measured directly on the file in question — neither a deeper
  elimination pass nor a full 60-second `cdcl` search budget gets
  `apn-sbox5-cut3-symmbreak.cnf` to a verdict. There's nothing to
  trade off in that file's favor by moving the default.

## The config system

### Design

`internal/params` (Go) / `params.rs` (Rust) each define a `Params`
struct (`CDCL` + `Preprocess` sub-structs, 13 fields total) with a
`Default()`, `Load(path)`, `Save(path, params)`, and
`Resolve(explicitPath)`. `Resolve` is the one both `main.go`/`main.rs`
actually call:

1. If `--internal-params=<path>` was given, load exactly that file —
   missing or malformed is a hard error (exit 1), never a silent
   fallback to defaults. If you asked for a specific file, getting
   your own typo's defaults back instead of an error would be worse.
2. Otherwise, if `.vibe_sat.json` exists in the current directory,
   load it.
3. Otherwise, every parameter keeps its built-in default.

A parameter a config file doesn't mention keeps its default —
`Load` starts from `Default()` and overlays only the keys present, so
a one-line override file is enough to change a single value.
Loading a config (explicit or implicit) is announced at
`--verbose=1` or higher (`"internal parameters: loaded from ..."`),
so a run's behavior is traceable from its own output.

`--reset-internal-params`/`-q` writes `Default()` to
`--internal-params`'s path (or `.vibe_sat.json` if that flag isn't
also given) and exits, without requiring `--input`/`--algorithm` —
the same required-argument bypass `--help` already gets. Go's
tokenizer already separates parsing from validation, so this bypass
lives in `validate()`. Rust's `clap` derive enforces `--input`/
`--algorithm` as required before a normal parse can even return, so
Rust instead pre-scans `argv` for `--reset-internal-params`/`-q`
before calling into `clap` at all — the same technique the existing
`wants_help` pre-scan already used for `--help`, not a new pattern.

Neither language changed any default numeric value; this stage is
purely about making 13 already-existing constants readable/writable
at runtime.

### The 13 exposed parameters

Full detail, including every constant considered and deliberately
*not* exposed (structural safety caps, sentinels, already-has-its-own
`--alg-params` path, etc.) with reasoning for each, is now in
[`docs/internal-parameters.md`](../docs/internal-parameters.md) —
`REPORT22.md` item 7's inventory, finally written down. Short version:
11 `cdcl` parameters (restart-schedule bases/growth factor, LRB's
alpha, both activity decays, the glue-clause LBD threshold, the
Glucose restart policy's window size and K, learned-clause
minimization's work-budget factor) and 2 `preprocess` parameters
(subsumption's and BVE's work-budget factors).

### Implementation notes

- Go's `lbdRecentBuf` (previously a fixed-size `[glucoseWindowSize]int`
  array, since Go array sizes must be compile-time constants) became a
  `[]int` slice sized at construction from the now-runtime
  `GlucoseWindowSize`; Rust's equivalent (`GlucoseState.recent_buf`)
  made the same change, `[usize; N]` → `Vec<usize>`.
- Rust has no `serde` dependency (only `clap`, `rand`,
  `crossbeam-deque`, `arc-swap`) — per this project's established
  "crates are inevitable but minimal" convention, `params.rs` hand-
  writes its own small JSON reader/writer for this fixed, two-level
  schema rather than pulling in a general-purpose JSON crate for a
  ~100-line problem.
- `activityRescaleThreshold`/`ACTIVITY_RESCALE_THRESHOLD` (`1e100`)
  deliberately stayed a plain constant in both languages — it's a
  floating-point overflow safety valve, not a search-quality knob;
  see `docs/internal-parameters.md`'s "not exposed" table for this
  and every other constant in the same category.

### Verification

Both languages: full build, vet/clippy, format-check, and test suite,
plus manual end-to-end CLI verification.

- Go: `go build ./...`, `go vet ./...`, `gofmt -l .` (clean), and
  `go test ./...` (all ten packages pass, including the new
  `internal/params` package).
- Rust: `cargo build --release`, `cargo build --all-targets`,
  `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`
  (all clean), and `cargo test --release` (234 passed, 0 failed).
- Manual smoke tests, both languages: `--reset-internal-params -v 1`
  writes a well-formed `.vibe_sat.json` with all 13 defaults;
  editing a value and running a real solve prints
  `"internal parameters: loaded from .vibe_sat.json"` and solves
  correctly; an explicit `--internal-params=<missing path>` fails
  hard (exit 1) rather than silently using defaults.
- **Cross-language compatibility, checked directly**: a
  `.vibe_sat.json` written by `--reset-internal-params` on the Go
  binary, hand-edited, was loaded correctly by the Rust binary (and
  the reverse) — both produce byte-identical config files and
  identical resolved parameter values from either one.

## The auto-tuning harness (`util/paramtune`)

A new standing tool at `util/paramtune`, alongside `util/benchcompare`
(same `prompts/PROMPT.md` Stage 18 carve-out for permanent tooling
that `go_src`/`rust_src` must not depend on). It drives
`util/benchcompare` — the existing cross-language measurement engine
— once per candidate value of a swept parameter, each time with a
fresh `.vibe_sat.json` set to that candidate and everything else at
default, and reports a table of every candidate tried plus the winner
(ranked: solved-file count first, then correctness issues, then total
elapsed time as the tiebreak — a sweep can never "win" by making the
solver less correct or complete).

The config-injection mechanism needed **no changes to
`benchcompare` itself**: `benchcompare`'s own subprocess call never
sets a working directory, so it inherits whatever directory it was
started from. `paramtune` just starts a `benchcompare` subprocess
from a scratch directory holding the candidate's `.vibe_sat.json` —
exactly `vibe_sat`'s own implicit-config-discovery path, reused
rather than routed around.

`--sweep` accepts a small mini-language: `;`-separated stages run in
sequence (coordinate descent — each stage's winner is fixed before
the next stage runs), `,`-separated parameter groups swept jointly
within one stage, `|`-separated candidate values. Coordinate descent
is the default because it's what this project's own tuning work has
actually needed so far (see "Design questions" below); the joint-grid
option exists for the rare case two parameters are suspected to
interact, without forcing every sweep to pay for a combinatorial
search it doesn't need.

Verified: `go build ./...`, `go vet ./...`, `gofmt -l .` clean,
`go test ./...` passing (unit tests use fakes for the subprocess
layer; real end-to-end validation used an actual `benchcompare` run
against `benchmark/uf20-91`).

## `bveWorkBudgetFactor` tuning (item 25)

### Method

`util/paramtune`'s own end-to-end sweep against the target file hit a
harness-level snag worth recording rather than a solver problem: this
environment's backgrounded-process monitor killed any run lasting
more than roughly 90-100 seconds with a "low memory" report,
regardless of the process's actual measured memory use (confirmed:
the same commands run to completion, with `/usr/bin/time -v`-measured
peak RSS well under 200 MB, when run in the foreground instead of
backgrounded) — a sandbox/tooling artifact of this session, not a
`vibe_sat` or `paramtune` defect. Rather than fight that, the sweep
itself was run directly and manually (constructing each candidate's
`.vibe_sat.json` by hand, invoking the Go binary foreground with
`/usr/bin/time`), isolated to a scratch copy of just the one target
file so a directory listing (`sat_comp/2018` has 400 files) couldn't
accidentally pull in unrelated large instances.

### Results

`apn-sbox5-cut3-symmbreak.cnf` (21,240 vars / 86,081 clauses), Go
binary, `--algorithm=cdcl` with a 2-second `cdcl` time budget (chosen
to isolate preprocessing's own cost/effect, not to expect a verdict):

| `bveWorkBudgetFactor` | Vars after BVE | Clauses after BVE | Eliminated | Wall clock (2s cdcl budget included) |
|---:|---:|---:|---:|---:|
| 500 (0.25x default) | 20,359 | 75,181 | 446 | 5.4s |
| **2000 (default)** | 19,124 | 72,998 | 1,681 | 15.8s |
| 5000 | 17,123 | 69,646 | 3,682 | 36.2s |
| 10000 | 14,570 | 65,961 | 6,235 | 68.5s |
| 20000 | 10,942 | 61,381 | 9,863 | 133.7s |

Every run, at every budget, reported `UNKNOWN` — the 2-second `cdcl`
window is far too short to expect otherwise on this instance; the
point of this table is preprocessing's own cost/reduction trade,
which scales roughly linearly with the budget (elimination count
increases, but so does wall-clock, in close proportion). Peak RSS
stayed modest throughout (39-51 MB across all five budgets) — no
memory-blowup risk at any tested value.

A follow-up direct check gave the `cdcl` phase a real chance: default
budget (2000), 60 real seconds of `cdcl` search time (on top of its
own ~14s of preprocessing) — **still `UNKNOWN`**. This matches
`REPORT31.md`'s own finding exactly (its 60-second UNSAT-status check
on this file also came back unfinished): the file is genuinely hard
for this `cdcl` implementation regardless of how deeply BVE
simplifies it first.

### Conclusion

**No change to the default.** Two independent reasons:

1. Raising `bveWorkBudgetFactor` costs wall-clock time roughly in
   proportion to the extra elimination it buys — there's no "free"
   region where a higher budget both eliminates substantially more
   and stays cheap.
2. More importantly: **it doesn't matter, because nothing tested gets
   this file solved anyway.** Whether preprocessing is shallow (446
   variables eliminated, 5.4s) or aggressive (9,863 eliminated,
   133.7s), and whether `cdcl` then gets 2 seconds or 60 real seconds,
   every configuration tried came back `UNKNOWN`. A parameter can only
   be worth tuning if some value of it changes the outcome; none of
   the five budgets tried changed this file's outcome at all.

This confirms rather than overturns `REPORT31.md`'s own framing:
`apn-sbox5-cut3-symmbreak.cnf` needs a different lever than a
work-budget knob — a length-based elimination heuristic (as
`REPORT31.md` itself suggested) or simply accepting it as this
project's one known unsolved pathological case — not a better
`bveWorkBudgetFactor`. `docs/internal-parameters.md` records this
file's tuning history against the parameter's entry for future
reference.

## Design questions (from `STAGE39.md`'s "Comments")

**Implicit or explicit config?** Both, together — not a choice
between them. An explicit `--internal-params=<path>` always wins when
given; `.vibe_sat.json` in the current directory is used automatically
if present; the built-in defaults apply otherwise. An explicit path
that's missing or malformed is a hard error, never a silent fallback —
see "Design" above for why.

**Tune parameters one at a time, or jointly?** One at a time
(coordinate descent), by default — matching the precedent already set
manually in `REPORT35.md`'s `glucoseK` sweep, and because this
project's tuning work to date has never found evidence these
parameters interact strongly enough to need joint search.
`util/paramtune` supports an explicit small joint grid for the rare
case two parameters are suspected to interact, but doesn't default to
one, since sweeping every combination of even a handful of
continuous-valued parameters against real benchmark instances gets
combinatorially expensive fast for a benefit this project hasn't yet
seen evidence it needs.

## Documentation

Per the standing instruction, checked this stage:

- `docs/internal-parameters.md` — new, item 15's deliverable.
- `docs/usage.md` — updated: `--internal-params`/`-c` and
  `--reset-internal-params`/`-q` documented alongside the other CLI
  options, matching the built-in `--help` text.
- `docs/background.md` — updated: a new bullet under "Other things
  worth knowing" notes that the restart-schedule/decay/threshold
  values described throughout the page are now runtime-configurable
  defaults, not fixed constants, with a pointer to
  `docs/internal-parameters.md`.
- `docs/references.md` — no update needed; no new paper or technique
  this stage, only infrastructure around existing, already-cited
  ones.
- `util/benchcompare/README.md` — a short cross-reference added,
  pointing to `util/paramtune` as a tool built on top of it.

Scratch files from this stage's manual verification (temporary
`.vibe_sat.json` files, a scratch copy of the target `.cnf`, ad hoc
sweep logs) were not committed.
