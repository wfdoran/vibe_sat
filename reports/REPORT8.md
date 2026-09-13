# Report: Stage 8

## Summary

Stage 8 is complete for both the Go and Rust versions of `vibe_sat`: a
preprocessing pipeline (unit propagation, pure literal elimination,
subsumption elimination, and bounded variable elimination, per
`STAGE5.md`'s preview and `REPORT7.md`'s item #1) now runs by default
before `hc`, `ws`, and `dfs` alike, with `--no-preprocessing`/`-x` to
disable it.

## Design

### Data structures and pipeline

Preprocessing is a new `internal/preprocess`/`preprocess.rs` package
operating on a plain mutable clause list (not the fixed, per-node
occurrence-list structures `hillclimb`/`dfs` use, since those assume a
clause set that never shrinks — preprocessing's whole point is to
shrink it). `Run`/`run` repeatedly applies, to a fixpoint (bounded by
a generous safety cap):

1. **Unit propagation**: find a clause with exactly one unassigned
   literal and force it true, removing now-satisfied clauses and
   stripping falsified literals from the rest, repeating until no
   unit clauses remain or a contradiction (an empty clause) is found
   — in which case preprocessing alone has **proven the problem
   UNSAT**, before any solving algorithm ever runs.
2. **Pure literal elimination**: a variable appearing with only one
   polarity across every remaining clause can be fixed to whichever
   value satisfies all of them, for free.
3. **Subsumption elimination**: if clause A's literals are a subset of
   clause B's, B is redundant (A already enforces at least as much)
   and is removed.
4. **Bounded variable elimination**, specifically the **NiVER** rule
   (Subbarayan & Pradhan, SAT 2004): a variable is eliminated by
   resolving every clause containing it positively against every
   clause containing it negatively, but *only* if doing so doesn't
   increase the total clause count (discarding tautological
   resolvents, which carry no information). This is the simpler,
   easier-to-bound cousin of SatELite's own variable elimination
   criterion (Eén & Biere, SAT 2005), chosen specifically because its
   correctness argument is easy to state and verify: it can never make
   the formula bigger.

### Variable renumbering, and why

A variable fixed (by unit propagation or pure literals) or eliminated
(by resolution) no longer appears in any remaining clause — but if it
stayed in the problem handed to `hc`/`ws`/`dfs` under its *original*
number, those algorithms would still see it as "unassigned" and could
waste branches/flips deciding a variable that no longer matters (this
is a real risk for `dfs`'s cheap `SelectVarFast` heuristic from Stage
6, which picks the lowest-numbered unassigned variable with no regard
for whether it's actually relevant). So the reduced problem's
surviving variables are renumbered into a dense range starting at 1,
and `hc`/`ws`/`dfs` never even see the eliminated ones. A
`new_to_original` map (plus the fixed values and the ordered list of
elimination steps) carries enough information to reconstruct a
solution back to the *original* variable numbering afterward.

### Reconstruction, and a real bug I found and fixed

Reconstruction processes eliminated variables in *reverse* elimination
order — each depends only on variables eliminated *after* it (which
are therefore already reconstructed) or variables that survived into
the final problem (already solved). For each eliminated variable,
trying `True` first and falling back to `False` only if some clause
it appeared in negatively isn't already satisfied by another literal
is a standard, provably-correct technique (the same logic used in
MiniSat-family solvers' own variable-elimination reconstruction).

While testing this against a hand-built example, I found a real bug:
a variable can disappear from the formula *without* ever being the
subject of its own elimination step, if every resolvent mentioning it
happens to be a tautology when some *other* variable is eliminated (a
genuine, not-hypothetical case — I traced through exactly this
happening on a 3-variable toy formula). My first implementation
defaulted such "evaporated" variables to `False` only *after* finishing
the reverse-elimination pass, which was too late: an earlier step (processed
later, in reverse) could need that variable's value to decide its own,
and was reading it while it was still `Unassigned`. The fix was to
move that default-to-`False` pass to *before* the reverse-elimination
loop, not after. I caught this with a unit test before it ever reached
real benchmark files, fixed it once in Go, and the Rust port (written
after the fix was understood) passed on the first try — worth
flagging since it's exactly the kind of subtle correctness issue this
project has hit before (the tautological-clause edge case in
`hillclimb.flip`, Stage 2) and is a good example of why I verify new
solver-adjacent code this thoroughly before trusting it.

## Verification

Given how easy it would be for a reconstruction bug like the one above
to silently produce a wrong "satisfying" assignment, I verified this
more heavily than a typical stage:

- Unit tests for each of the four techniques in isolation, plus the
  full pipeline, in both languages (mirrored test suites).
- A throwaway Go harness (not committed, per the project's cleanup
  convention — built, used, and deleted) that ran preprocessing
  followed by `dfs` and `hc` against **550 real benchmark files**
  (both SAT- and UNSAT-labeled), independently re-checking every
  reconstructed solution against the *original* CNF file's clauses:
  **0 false-UNSAT claims, 0 reconstruction violations.**
- An end-to-end CLI sweep (through the actual built binaries, not
  library calls) across 80 `hc`/`dfs` runs with real output files,
  independently verified with a separate Python script: **0
  violations**, and **0 Go/Rust mismatches**.
- Confirmed `--no-preprocessing` and the default (preprocessing on)
  agree on every verdict across 20 files, and that toggling it doesn't
  change `hc`'s or `dfs`'s correctness, only performance.

## An honest finding: limited impact on these benchmarks

Across the 550-file sweep, preprocessing eliminated only ~1.2
variables per file on average on the SATLIB `uf`/`uuf` random 3-SAT
benchmarks this project uses. This is expected, not a bug: uniform
random 3-SAT near the satisfiability phase transition is specifically
constructed to have almost no exploitable structure (no easy unit
chains, no pure literals, few beneficial resolutions) — it's
well-documented in the preprocessing literature (including SatELite's
own original evaluation) that these techniques pay off far more on
*structured* industrial instances (circuit verification, cryptographic
constraints, etc.) than on random k-SAT. I'm reporting this plainly
rather than only showcasing favorable numbers: the feature is correct
and does what it's supposed to, it just doesn't have much to chew on
in this particular benchmark set.

## Command line arguments

`--no-preprocessing`/`-x` is a boolean flag (no value) that disables
preprocessing, defaulting to "run it." This needed one new piece of
machinery in Go's hand-rolled tokenizer: `flagSpec.maxValues` can now
be `0`, handled as its own case in `tokenize` (consume the flag token,
no value, error on an inline `=value`) — previously only `--help`/`-h`
were zero-value, and those were special-cased as a pre-scan rather than
integrated into the general flag table. Rust's clap handles a `bool`
field natively via its derive macro, as usual requiring no comparable
plumbing.

## Testing

- Go: `go vet`, `gofmt -l`, and `go test ./...` (8 packages) are all
  clean. New/updated tests: all four techniques in
  `internal/preprocess`, the reconstruction edge case described
  above, the `--no-preprocessing`/`-x` flag (long form, short form,
  rejecting an inline value, not swallowing a following flag).
- Rust: `cargo fmt --check` and `cargo clippy --all-targets -- -D
  warnings` are clean; `cargo test` passes all 109 tests.
- Manual/scripted verification as described above.

## Open questions / notes for you

- No language/toolchain version changes needed.
- Preprocessing's effect is modest on this project's random 3-SAT
  benchmark set specifically (see above) — if you'd like to see it
  shine, it would need a structured/industrial-style CNF file, which
  isn't in `benchmark/` today.
- Preprocessing proving UNSAT on its own (independent of which
  algorithm was requested) is surfaced as `UNSAT` even for `hc`/`ws`,
  which otherwise can only ever report `UNKNOWN`. This wasn't
  explicitly specified in `STAGE8.md`, but seemed clearly better than
  silently running a doomed search — flagging it in case it's not
  what you had in mind.
