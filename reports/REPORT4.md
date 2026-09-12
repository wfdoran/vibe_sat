# Report: Stage 4

## Summary

Stage 4 is complete for both the Go and Rust versions of `vibe_sat`.
Both now support a second algorithm, `--algorithm=ws`, implementing
WalkSAT alongside the existing simple hill-climb (`hc`).

## Algorithm selection

STAGE4.md asked me to pick whichever hill-climb-like algorithm seemed
best, mentioning tabu search and WalkSAT as two obvious candidates. I
chose **WalkSAT** (Selman, Kautz & Cohen, "Noise Strategies for
Improving Local Search," AAAI 1994; sometimes called "WalkSAT/SKC"):

- It is one of the most thoroughly studied and consistently effective
  incomplete local-search methods for SAT, and it was originally
  developed and evaluated on exactly the kind of instances in our
  `benchmark/` directory — uniform random 3-SAT near the
  satisfiability phase transition (the SATLIB `uf*`/`uuf*` sets). That
  makes it a well-matched, literature-grounded choice rather than an
  arbitrary pick.
- It reuses almost all of the machinery already built in Stage 2 (the
  occurrence lists, the per-clause true-literal counts, the
  incremental flip/score bookkeeping) plus one new small primitive
  (the "break count" of a variable — how many currently-satisfied
  clauses a flip would break), which the existing data structures
  already make cheap to compute. This made it genuinely easy to add,
  as the stage asked for.
- Tabu search was the other candidate I considered. It's a reasonable
  alternative, but introducing a tabu list/tenure adds a tuning
  parameter whose right value is less standardized in the literature
  than WalkSAT's noise parameter, and published comparisons on random
  3-SAT don't show it clearly outperforming WalkSAT-family methods for
  a similar level of implementation effort — so the extra complexity
  didn't seem to earn its keep for this stage.

**Empirical check, not just literature**: to sanity-check this choice
against our own benchmark set (not just trust the literature), I ran
both algorithms on the harder `uf100-430` instances (100 variables,
430 clauses) with a 1-second search budget per instance:

| Algorithm | Solved out of 30 (`uf100-430`, 1s budget) |
|---|---|
| `hc` (Stage 2) | 3 |
| `ws` (this stage) | 30 |

WalkSAT solved every instance tried within a second; the simple
hill-climb got stuck at a local optimum on the large majority of them.
This matches the literature's central claim about why noise/random-walk
local search exists in the first place, and confirms the choice is a
real improvement on this project's own data, not just a textbook one.

## The WalkSAT algorithm, as implemented

Per try: generate a random complete assignment, then repeatedly —

1. If every clause is satisfied, stop; SAT found.
2. Otherwise, pick one of the currently *unsatisfied* clauses
   uniformly at random.
3. With probability `noise_percent / 100`, flip a uniformly random
   variable of that clause ("noise" / random walk step). Otherwise,
   flip whichever variable of that clause has the smallest **break
   count** — the number of currently-satisfied clauses that would
   become unsatisfied by flipping it — breaking ties uniformly at
   random (via reservoir sampling).
4. Always commit the flip (unlike `hc`, which only keeps flips that
   strictly improve the score). This is precisely what lets WalkSAT
   walk out of the local optima that trap plain hill-climbing.

If `max_flips_per_try` flips pass without solving, give up and start a
fresh try with a new random assignment. Tries continue until either a
try succeeds, `num_tries` tries have been attempted, or the time limit
is reached — all symmetric with `hc`'s restart logic from Stage 2.

## Data structure changes

Both languages' `climbState`/`ClimbState` (shared by `hc` and `ws`,
since WalkSAT is explicitly a "hill-climb-like" algorithm per
STAGE4.md's own title) gained:

- A reference to the `Problem` itself (needed to look up the literals
  of a randomly chosen unsatisfied clause — previously `climbState`
  only needed the occurrence lists, not the clauses themselves).
- An incrementally-maintained list of currently unsatisfied clause
  indices (`unsatClauses`/`unsat_clauses`), with O(1) insertion and
  removal (a classic swap-with-last-element trick, paired with a
  position index `unsatPos`/`unsat_pos`) so WalkSAT can pick a random
  unsatisfied clause in O(1) instead of scanning every clause on every
  step.
- A `breakCount`/`break_count` method: a read-only, non-mutating query
  reusing the same occurrence lists `flip` already relies on.

In Go, both algorithms live in one `hillclimb` package across two
files (`hillclimb.go`, `walksat.go`), sharing `climbState`'s unexported
fields and methods freely, since Go's privacy is package-scoped. Rust
has no equivalent of "same package, different file" — privacy in Rust
is scoped to a module and its descendants — so to let WalkSAT reuse
`ClimbState`'s private internals the same way, I converted
`hillclimb.rs` into a directory module (`hillclimb/mod.rs` +
`hillclimb/walksat.rs`, the latter declared as `pub mod walksat;`
*inside* `mod.rs`, making it a descendant module rather than a
sibling). This was the one real structural difference this stage
required between the two languages, driven entirely by how each
language's visibility rules work, not by any difference in the
algorithm itself.

## Command line arguments

No new flag names, as STAGE4.md specified. `--algorithm=ws` reuses
`--alg-params`/`-p` with up to three positional values:

1. number of tries (restarts) — same role as `hc`'s single value
2. max flips per try before giving up (default 10000 if omitted)
3. noise percent, 0-100 (default 50 if omitted)

As with `hc`, at least one of `--alg-params` or `--time-limit-secs` is
required. The values are strictly positional (to set the noise percent
you must also supply the tries and max-flips values before it) — this
was the simplest rule consistent with `--alg-params`'s existing
design from Stage 2, and is documented in `--help`.

`--help`/`-h` output and the CLI validation in both languages were
updated per STAGE4.md's explicit instruction to keep it current.

## Testing

- Go: `go vet`, `gofmt -l`, and `go test ./...` are all clean. New
  tests cover the unsatisfied-clause-set bookkeeping (that it matches
  a from-scratch recomputation after flips, including through the
  tautological-clause edge case from Stage 2), `breakCount`, greedy
  vs. noise variable selection in `chooseFlipVariable`, and `RunWalkSat`
  against both a trivially satisfiable and a trivially unsatisfiable
  formula (the latter deterministically, avoiding RNG-seed-dependent
  flakiness, the same approach used for `hc` in Stage 2).
- Rust: `cargo fmt --check` and `cargo clippy --all-targets -- -D
  warnings` are clean; `cargo test` passes all 69 tests (the
  equivalent new tests to Go's, plus the pre-existing suite).
- Cross-checked CLI validation error messages/behavior between the two
  languages for `ws` (missing stopping criterion, out-of-range noise
  percent, etc.) — they currently even match verbatim, though that
  isn't a requirement.
- Ran both binaries' `ws` across 60 randomly sampled benchmark files
  with no crashes, and did the `hc`-vs-`ws` comparison above on both
  languages independently (Rust showed the same 3-vs-30-ish gap as
  Go).
- Found and removed a second stray compiled Go binary
  (`go_src/cmd/vibe_sat/vibe_sat`) left by another un-redirected
  `go build ./...` during this stage's own testing — already covered
  by the `.gitignore` added in Stage 3, so it was never at risk of
  being committed, just disk clutter. Cleaned up before finishing.

## Open questions / notes for you

- No language/toolchain version changes needed.
- `DefaultMaxFlipsPerTry`/`DEFAULT_MAX_FLIPS_PER_TRY` = 10000 and
  `DefaultNoisePercent`/`DEFAULT_NOISE_PERCENT` = 50 are my own
  defaults (50% noise is a commonly cited good default for WalkSAT/SKC
  on random 3-SAT in the literature; 10000 flips is simply generous
  for instances this size). Happy to adjust if you'd like different
  defaults.
- `--alg-params`'s strictly-positional rule (can't set noise percent
  without also specifying tries and max-flips) is a minor usability
  wrinkle worth knowing about, flagged above.
