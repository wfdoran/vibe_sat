# Report: Stage 14

## Summary

Stage 14 is complete for both the Go and Rust versions of `vibe_sat`:
`cdcl` now saves phases. When a variable becomes unassigned during
backtracking, its last value is remembered; the next time that
variable is chosen as a decision, `cdcl` guesses the same polarity
again instead of always guessing `False`. Per `STAGE14.md`, this is
unconditionally on for every `SelectVar` variant, with no
`--alg-params` toggle — the stage explicitly says there's no reason
to make it optional.

This is a small, contained change compared to Stages 11-13, and the
benchmark below shows why the reference material (Pipatsrisawat &
Darwiche's 2007-era MiniSat/RSat-lineage discussion, cited in
`STAGE14.md`) calls it "a consistently measured win": on this
project's own 250-variable UNSAT benchmark sample, it took the number
of instances solved within a fixed time budget from 3/10 to 7/10.

## Design

Phase saving touches exactly two places, both already central to
`cdcl`'s control flow:

- **`backtrackTo`/`backtrack_to`** (where a variable becomes
  unassigned): right before overwriting a trail variable's value with
  `Unassigned`, its current value is copied into
  `solver.savedPhase[v]`/a `saved_phase` array threaded through `run`
  (Go: a new `solver` field, alongside `varActivity`/`lrbQ` from
  Stages 12-13; Rust: a new local in `run`, following the same
  free-function pattern established since Stage 11). This runs for
  every unassigned variable regardless of `SelectVarVariant`, since
  `STAGE14.md` asks for it unconditionally.
- **`decide`/the decision step in `run`** (where a new branching
  variable is chosen): once `SelectVar` (whichever variant) has
  picked *which* variable to branch on, the polarity to try is read
  from `savedPhase[v]`/`saved_phase[v]` instead of always being
  `False`. A variable that has never been assigned before still
  defaults to `False`, since `savedPhase`/`saved_phase` starts at
  that value for everything (Go: the zero value of `assign.Value`
  happens to be `False`, so a plain `make` needs no explicit
  initialization loop, documented explicitly in the field's doc
  comment so this isn't a silent coincidence; Rust: `Value` has no
  such zero-value convention, so `saved_phase` is built with
  `vec![Value::False; num_vars + 1]` directly).

In Rust, the decision-literal computation was pulled out into its own
small `decision_literal(v, saved_phase)` function rather than left
inline in `run`'s loop, purely so it has something directly
unit-testable to call, mirroring Go's `decide` (already its own
method, and thus already directly testable).

Phase saving is orthogonal to *which* variable gets chosen — it only
ever affects the guessed *polarity* once a variable has already been
selected by `SelectVarWeighted`/`Fast`/`Vsids`/`Lrb` — so it needed no
interaction with any of Stages 11-13's per-variant bookkeeping beyond
sharing the same unassignment hook point `backtrackTo`/`backtrack_to`
already provided.

## Verification

- **Unit tests** (26 per language, mirrored, +3 net from Stage 13's
  23): a direct test that `backtrackTo`/`backtrack_to` saves a
  variable's phase correctly on unassignment; a direct test that
  `decide`/`decision_literal` guesses the saved phase when one exists;
  and a direct test confirming the `False` fallback when no phase has
  been saved yet. `go test ./...` (9 packages) and `cargo test` (148
  tests, `cargo fmt --check`, `cargo clippy --all-targets -- -D
  warnings`) are all clean.
- **Correctness sweeps**: SAT/UNSAT ground-truth cross-check across
  all four `SelectVar` variants (120/120 correct); independent,
  from-scratch Python solution verification across both languages, all
  four variants, and combined with memory limits (90/90 clean per
  language); direct Go-vs-Rust comparison across all four variants
  plus the default (`-p` omitted): **0 mismatches across 100 runs.**
  Phase saving changes *how* the search finds its way, never *what*
  it concludes.

## Benchmark: does it actually help?

I re-ran the exact before/after methodology used in prior stages: a
pre-Stage-14 baseline binary (via `git stash`, building, then
restoring) against the current one, both using `-p 2` (VSIDS, the
current default), on the same 15 sampled `uf250-1065` (SAT, 20s cap)
and 10 sampled `uuf250-1065` (UNSAT, 45s cap) files Stage 13's
benchmark used:

| Set | Binary | Solved | Mean (solved) | Median (solved) |
|---|---|---|---|---|
| SAT (`uf250-1065`) | no phase saving | 12/15 | 4.93s | 2.50s |
| SAT (`uf250-1065`) | **phase saving** | 12/15 | 4.20s | 2.54s |
| UNSAT (`uuf250-1065`) | no phase saving | 3/10 | 32.8s | 31.1s |
| UNSAT (`uuf250-1065`) | **phase saving** | **7/10** | 33.8s | 37.1s |

On the SAT sample, phase saving makes essentially no difference —
same solved count, and the mean/median move in opposite directions by
small amounts consistent with noise on a 15-file sample. On the UNSAT
sample, though, it's a clear, substantial win: **4 additional
instances solved within the same 45-second budget**
(`uuf250-01`, `-013`, `-015`, `-017`), and two of the three instances
already solved either way got meaningfully faster (`uuf250-012`:
30.5s → 22.0s; `uuf250-016`: 31.1s → 17.4s). The mean/median among
solved instances actually go up slightly, but that's an artifact of
comparing different sets of instances (the newly-solved ones are
harder, by construction, since they needed the full nearly-45s budget
to finish) — the file-by-file comparison is the honest read here, and
it's unambiguously favorable.

This lines up with why phase saving is specifically well-suited to
proving unsatisfiability: reaching a *complete* proof of UNSAT means
the search must, in effect, explore every relevant branch of the
search tree, and re-using a variable's previously successful polarity
tends to let already-explored sub-trees close out faster on
subsequent visits (from different decision paths / after backjumping)
rather than re-litigating the same polarity choice from scratch every
time. A satisfying assignment, by contrast, can be found via any
lucky-enough path through the tree, so there's less structural reason
to expect a systematic win on the SAT side specifically -- consistent
with what the sample shows.

## Command line arguments

None. Per `STAGE14.md`: "I don't see any reason not to have phase
saving be the default. So, no change to any of the command line
parameters." Phase saving is unconditionally active for `cdcl`
regardless of `--alg-params`.

## Testing

- Go: `go build ./...`, `gofmt -l .`, `go vet ./...`, and
  `go test ./...` (9 packages) are all clean.
- Rust: `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`,
  and `cargo test` (148 tests) are all clean.
- Manual/scripted verification as described above.

## Open questions / notes for you

- No language/toolchain version changes needed.
- No changes to `dfs`, `hc`, or `ws` in either language, and no new
  CLI surface, exactly as `STAGE14.md` asked.
- The SAT-side benchmark sample (15 files) showed no clear effect
  either way; I wouldn't read that as phase saving being neutral in
  general so much as this specific small sample not being where its
  benefit shows up -- the UNSAT side's result is the one I'd trust as
  representative of the technique's value here.
