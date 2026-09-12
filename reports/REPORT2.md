# Report: Stage 2

## Summary

Stage 2 is complete for both the Go and Rust versions of `vibe_sat`.
Both implement the basic hill-climbing local search described in
`STAGE2.md`, with the same data structures, the same incremental
scoring technique ("Tilt"), the same verbose-level behavior, and the
same command line arguments (including the variable-arity
`--alg-params`/`-p` flag). Both were exercised against the benchmark
set (including one confirmed UNSAT instance, correctly reported as
UNKNOWN) and produce consistent results.

## Data structures

- **`assign`/`assignment`** (new package/module): a `Value` enum with
  `False`, `True`, and `Unassigned` (the spec's suggested third state
  for future partial assignments), and an `Assignment` indexed
  directly by variable number. `NewRandom`/`new_random` draws a
  uniformly random complete assignment from an injected RNG.
- **`occurrence`** (the "Tilt" precomputation from STAGE2.md): for
  every variable, the list of clause indices in which it appears
  positively and the list in which it appears negatively. Built once
  per problem in O(total literals).
- **`hillclimb`**: the search itself. A `climbState`/`ClimbState`
  holds the current assignment, a per-clause count of currently-true
  literals, and the running score (number of satisfied clauses). A
  single `flip`/`flip` method updates the count/score for just the
  clauses touching the flipped variable — using the occurrence lists —
  rather than rescanning the whole formula, and is structured so that
  flipping the same variable twice exactly undoes the first flip. Each
  candidate flip during a sweep is therefore evaluated by actually
  applying it, checking whether the score improved, and reverting (by
  flipping back) if not. This also handles the edge case of a
  tautological clause (one variable appearing with both polarities in
  the same clause) correctly, which a naive "compute the delta without
  mutating" approach gets wrong — covered by a dedicated test in both
  languages.
- **`solution`**: writes a satisfying assignment in the DIMACS
  solution format (`s SATISFIABLE` / `v <literals> 0`).

## Hillclimb algorithm

Implemented exactly as pseudocoded in `STAGE2.md`: for each start (up
to `num_starts` and/or until the time limit), generate a random
assignment, then repeatedly sweep over the variables in a random order
and flip any variable whose flip strictly increases the score, keeping
the change only if it helped; stop sweeping when a full pass makes no
improvement. If the score equals the number of clauses, report SAT,
write the solution, and stop immediately (no further starts). Time is
only checked once per start, not inside the inner loop, per the spec.

Two interpretive decisions, called out in case they don't match your
intent:

- **"new best score ... across all starts" (verbose >= 2)**: checked
  after every kept flip of every start (and the initial random
  assignment of each start), not just once per start — "every time it
  achieves a new best score" reads as an event that can happen
  mid-climb, since score only increases monotonically within a single
  start.
- **"every time it gets stuck and has to restart" (verbose >= 3)**: I
  print this whenever a start's inner loop terminates without a full
  solution, including the very last start of the run (even though
  there's technically no "restart" left afterward) — it seemed more
  useful/consistent to always report the outcome of a start than to
  special-case the final one.

## Command line arguments

Added `--algorithm`/`-a` (required, only `"hc"` allowed),
`--output`/`-o` (optional), `--time-limit-secs`/`-t` (optional,
positive integer seconds), and `--alg-params`/`-p` (1 to 3 values; for
`hc`, at most one value — the number of starts — and at least one of
`--alg-params` or `--time-limit-secs` is required).

**`--alg-params`'s variable arity doesn't fit either language's
simple-flags tooling**, so the two implementations diverge under the
hood more than in Stage 1:

- **Go**: the standard `flag` package assumes exactly one value per
  flag, so I replaced it with a small hand-written tokenizer
  (`internal/cliargs`) that handles `--name=value`, `--name value`,
  and `-x value` for every flag, and specially lets `--alg-params`/`-p`
  greedily consume up to three following tokens, stopping at the next
  token that looks like a flag (a `-`-prefixed token that isn't a
  negative number).
- **Rust**: `clap`'s derive API supports this natively via
  `num_args = 1..=3`, combined with `allow_negative_numbers = true` so
  that a negative number-of-starts value isn't mistaken for a flag
  (this needed to be `allow_negative_numbers`, not the more obvious
  `allow_hyphen_values` — the latter also swallowed a genuine
  following flag like `-v`, which a quick manual test against the
  benchmark set caught).

Both were checked against the same set of representative command
lines (missing required args, bad algorithm name, too many
`--alg-params` values, negative values, a stray positional argument,
multi-value space-separated forms stopping at the next flag) and
behave the same way, even though the error *text* differs between the
two (expected, since one is hand-rolled and the other is clap's own
wording).

## Output ("DIMACS solution format")

This term isn't part of the DIMACS input spec; I used the common SAT
competition output convention: a status line (`s SATISFIABLE`)
followed by one value line (`v <lit1> <lit2> ... 0`) with one signed
literal per variable. Per `STAGE2.md`, this is written to
`--output`'s file if given, or to stdout if verbose >= 1 and no
`--output` was given; if neither condition holds, nothing is written
(this only matters when a solution was actually found — an UNKNOWN
result has no assignment to write).

## Issue found and fixed: clap's `allow_hyphen_values` was too broad

While cross-checking the two implementations against the same command
lines, `-p 3 -v 3` failed in Rust with "invalid digit found in string"
— `allow_hyphen_values` makes clap accept *any* `-`-prefixed token as
an `--alg-params` value, so it tried to swallow `-v` itself and then
failed to parse it as an integer. Switching to clap's narrower
`allow_negative_numbers` (which only treats tokens that parse as
negative numbers as values, leaving other `-`/`--` tokens as flags)
fixed it and now matches the Go tokenizer's behavior. Caught by manual
testing against real benchmark files, not by the unit tests — a
reminder that CLI parsing libraries' options can have sharper edges
than their names suggest.

## Randomness

Both languages need an RNG for the initial assignment and each sweep's
variable order. To keep this dependency-injectable (so unit tests can
use fixed seeds) while still giving each real run unpredictable
behavior:

- **Go**: `math/rand/v2` (no external package), with a `*rand.Rand`
  seeded from `crypto/rand` at program start and threaded through as a
  plain argument — never a package-level/global generator.
- **Rust**: the `rand` crate (0.10.2; unavoidable per `PROMPT.md` since
  Rust's standard library has no RNG, and it's a general-purpose crate,
  not SAT-specific), with `StdRng::from_rng(&mut rand::rng())` seeded
  at program start, also threaded through as a plain argument.

## Testing

- Go: `go test ./...` — 6 packages, all passing (new packages
  `assign`, `occurrence`, `hillclimb`, `solution`, plus extended
  `cliargs` tests). `go vet` and `gofmt` are both clean.
- Rust: `cargo test` — 52 tests across all modules, all passing.
  `cargo clippy --all-targets -- -D warnings` and `cargo fmt --check`
  are both clean (fixed a few `collapsible_if`/`needless_range_loop`
  lints along the way, including adopting Rust's new `if let ... &&`
  let-chains syntax now that it's stable).
- Deterministic correctness tests in both languages include: an
  always-satisfiable formula (single-literal clauses) to confirm
  `Run`/`run` reaches `Satisfiable`/`satisfiable`, and a trivially
  *unsatisfiable* formula (`x1` and `NOT x1`) to confirm it correctly
  reports `UNKNOWN` regardless of how many restarts it's given — this
  avoids any flakiness from testing "does it find a known-hard SAT
  instance in N starts," which would depend on the RNG seed.
- Manually ran both binaries against real benchmark files at every
  verbose level, against a confirmed-UNSAT instance
  (`benchmark/uuf50-218`), with `--output`, and against a batch of 40
  randomly sampled benchmark files to confirm neither binary crashes
  or exits non-zero on real input.

## Open questions / notes for you

- No language/toolchain version changes needed.
- Let me know if either interpretive decision above (the "new best
  score" and "stuck" print timing) isn't what you had in mind — they
  were the most natural reading of the pseudocode to me, but STAGE2.md
  doesn't pin them down exactly.
- `hillclimb.Result`/`hillclimb::SolveResult` carries a `Starts`/
  `starts` and `Score`/`score` field that `main` doesn't currently
  print anywhere (STAGE2.md doesn't ask for it); Rust's compiler flags
  unused-but-`pub` fields in a binary crate as dead code where Go's
  does not, so I added `#[allow(dead_code)]` with a short comment
  rather than deleting fields that seem likely to be useful
  diagnostic/logging output later.
