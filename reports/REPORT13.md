# Report: Stage 13

## Summary

Stage 13 is complete for both the Go and Rust versions of `vibe_sat`:
`cdcl`'s `SelectVar` now offers two modern, activity-based branching
heuristics on top of the two it already delegated to `dfs`:

- **VSIDS** (Moskewicz, Madigan, Zhao, Zhang & Malik, "Chaff:
  Engineering an Efficient SAT Solver," DAC 2001) — the classic
  approach: every variable touched while resolving a conflict gets
  its activity bumped, decayed over time so recent conflicts count
  for more than old ones.
- **LRB** (Liang, Ganesh, Poupart & Czarnecki, "Learning Rate Based
  Branching Heuristic for SAT Solvers," SAT 2016), simplified — every
  variable's *learning rate*, how often it has recently participated
  in producing a learned clause per conflict it has been assigned
  for, is tracked instead of a decayed count.

Per `STAGE13.md`, these are `--alg-params`'s first value 2 and 3
respectively, for `--algorithm=cdcl` only (`dfs` keeps its existing
0/1-only range, since VSIDS/LRB need conflict-driven learning's
bookkeeping to have anything to work from). `STAGE13.md` explicitly
left the choice of default up to this implementation — see "The
default: a literature choice overturned by measurement" below for why
that took two passes to get right.

## Design

### VSIDS: reusing Stage 12's clause-activity machinery for variables

Stage 12 already built MiniSat-style activity bookkeeping for
*clauses* (bump-on-use during conflict analysis, O(1) decay via a
growing increment rather than rescaling every entry). VSIDS is the
same mechanism applied to *variables* instead: every time `analyze`
marks a variable `seen` while resolving a conflict — the exact same
hook point Stage 12 used to bump clause activity — its variable
activity is bumped by the current increment (`varActivityIncrement`/
`VsidsState.increment` in Go/Rust respectively). Once per conflict,
that increment is grown by `1/varActivityDecay` (0.95, MiniSat's own
default for variables — deliberately different from clause activity's
0.999, since VSIDS conventionally decays faster). Decisions then pick
the highest-activity unassigned variable via a plain linear scan
(`selectVarByActivity`/`select_var_by_activity`), the same asymptotic
cost as the `Weighted` heuristic's own clause scan.

### LRB: participation rate as a learning-rate signal

LRB's core idea, as implemented here: every variable tracks
`participated` (how many conflicts it has contributed a literal to
since it was last assigned) and `assignedAtConflict` (how many
conflicts had already happened when it was assigned). The moment a
variable becomes *unassigned* — inside `backtrackTo`/`backtrack_to`,
the natural place this information becomes final — its learning rate
`r` is computed as `participated / (numConflicts - assignedAtConflict)`,
and its `Q`-value is nudged toward `r` by a fixed weight (`lrbAlpha`/
`LRB_ALPHA` = 0.4, an exponential moving average), after which
`participated` resets to 0 for its next stint as an assigned
variable. Decisions pick the highest-`Q` unassigned variable, via the
same `selectVarByActivity` helper VSIDS uses.

Two things from the original paper are deliberately **not**
implemented, and are called out in the code: the "reason side rate"
bonus (an extra reward for variables that are *reasons* for other
bumped variables, not just directly bumped themselves), and the
paper's annealed learning-rate schedule (it decays `alpha` over the
course of the search; this implementation keeps it fixed). Both are
documented simplifications, not oversights — see "An honest look at
the numbers" below for why they may matter more than I originally
expected.

### CLI and default

`--alg-params`'s first value, for `--algorithm=cdcl` only, now
accepts 0-3 instead of 0-1 (`dfs`'s own range is untouched). Both
languages' CLI validation and help text were updated accordingly.

## The default: a literature choice overturned by measurement

`STAGE13.md` explicitly left the default up to this implementation.
`STAGE13.md`'s own cited numbers (SAT Competition 2009-2014: LRB 1279
solved vs. VSIDS 1179 vs. CHB 1235) point toward LRB, so that's what I
initially shipped as the default. Before finalizing, though, I ran an
actual benchmark comparison on *this project's* benchmark set — 15
sampled `uf250-1065` (SAT) files at a 20s cap and 10 sampled
`uuf250-1065` (UNSAT) files at a 45s cap, comparing Weighted (`-p 0`,
the pre-Stage-13 default), VSIDS (`-p 2`), and LRB (`-p 3`):

| Variant | SAT solved (of 15) | SAT median (solved) | UNSAT solved (of 10) | UNSAT median (solved) |
|---|---|---|---|---|
| Weighted | 5 | 1.18s | 0 | -- |
| **VSIDS** | **12** | 2.50s | **3** | 31.5s |
| LRB | 6 | 3.25s | 0 | -- |

VSIDS is unambiguously the strongest of the three here — more than
double Weighted's and LRB's solved count on the SAT sample, and the
*only* one of the three to solve any of the sampled UNSAT instances at
all. A file-by-file comparison confirms this isn't an artifact of
which files happened to be sampled: VSIDS solves several files
(`uf250-010`, `-011`, `-012`, `-014`, `-018`) that *both* Weighted and
LRB time out on.

This directly contradicts the literature-motivated choice I shipped
first. **I changed the default to VSIDS** rather than keep LRB and
merely footnote the discrepancy, because real measurement on the
benchmarks this project actually uses should outweigh a priori
reasoning from a different benchmark distribution, especially once
the two are in direct conflict. Every mention of the default in both
languages' code, help text, and doc comments reflects VSIDS now; `-p 3`
still gets you LRB explicitly if you want it.

### Why the discrepancy is not surprising, in hindsight

This project has hit this exact shape of result before:
`REPORT8.md` found preprocessing barely helps on this benchmark set's
uniform random 3-SAT instances specifically, because they're
constructed to have almost no exploitable structure — and the SAT
Competition instances LRB's paper was tested against are a very
different, much more structurally diverse mix (industrial, crafted,
*and* random). LRB's whole premise is that recent participation rate
carries more signal than either raw activity count (VSIDS) or
structural clause weight (Weighted) — a bet that's more likely to pay
off when conflicts vary in "shape" across the run, which a single
narrow distribution near the 3-SAT phase transition may simply not
offer as much of. On top of that, this implementation's LRB omits the
reason-side-rate bonus and uses a fixed rather than annealed alpha —
both real simplifications relative to the paper, and each a plausible
contributor to the gap on top of the benchmark-distribution mismatch.
I can't cleanly separate "LRB doesn't suit this instance distribution"
from "this LRB implementation is missing pieces that would have
helped" from this data alone.

## Verification

- **Unit tests** (23 per language, mirrored, +5 net from Stage 12's
  18): a direct test of `selectVarByActivity`/`select_var_by_activity`
  (ties, skipping assigned variables); a hand-traced VSIDS test
  reusing the same formula from Stage 11's first-UIP test, confirming
  the two variables touched while resolving that conflict both end up
  with positive activity; a hand-computed LRB test (`participated=3`
  over an `interval=5`, checked against `LRB_ALPHA * (3/5)` exactly,
  plus confirming the reset to 0 afterward); and end-to-end pigeonhole
  tests for both new variants, mirroring the ones already established
  for `Weighted`/`Fast`. The two existing `Run`/`run`-level tests that
  loop over every variant now cover all four instead of two, for free.
  `go test ./...` (9 packages) and `cargo test` (145 tests, `cargo fmt
  --check`, `cargo clippy --all-targets -- -D warnings`) are all
  clean.
- **Correctness sweeps**: SAT/UNSAT ground-truth cross-check across
  all four variants (120/120 correct); independent, from-scratch
  Python solution verification across both languages, all four
  variants, and combined with tight memory limits to confirm Stage
  12's clause-deletion logic doesn't interact badly with the new
  per-variable bookkeeping (90/90 clean per language); direct
  Go-vs-Rust comparison across all four variants, with and without
  memory limits, including the new default (`-p` omitted): **0
  mismatches across 280 total runs** (180 explicit-variant + 100 with
  the new default).

## Command line arguments

`--alg-params`'s first value, for `--algorithm=cdcl` only: 0 and 1
keep their existing meaning (`dfs`'s Weighted/Fast, delegated
unchanged); 2 selects VSIDS, 3 selects LRB. Defaults to 2 (VSIDS) if
`--alg-params` is omitted entirely for `cdcl` — unlike `dfs`, which
still defaults to 0. `dfs`'s own `--alg-params` range and default are
untouched. Help text and CLI validation in both languages were
updated accordingly.

## Testing

- Go: `go build ./...`, `gofmt -l .`, `go vet ./...`, and
  `go test ./...` (9 packages) are all clean.
- Rust: `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`,
  and `cargo test` (145 tests) are all clean.
- Manual/scripted verification as described above.

## Open questions / notes for you

- I'd treat "LRB underperforms here" as provisional, not a final
  verdict on the technique: adding the reason-side-rate bonus and/or
  the paper's annealed alpha (both flagged as omissions above) would
  be the natural next experiment before concluding LRB just doesn't
  suit this benchmark set. I did not attempt either, to keep this
  stage's scope to what `STAGE13.md` asked for.
- The benchmark comparison above used a modest sample (15 SAT + 10
  UNSAT files, capped at 20s/45s) to keep this stage's runtime
  reasonable, following the same tradeoff prior stages' benchmarks
  made. The file-by-file margin (VSIDS solving 5+ files neither other
  variant touches) is wide enough that I'm confident in the direction
  of the result, if not the exact percentages a larger sample would
  show.
- No language/toolchain version changes needed.
- No changes to `dfs`, `hc`, or `ws` in either language.
