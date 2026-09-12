# Report: Stage 5

## Summary

Stage 5 is complete for both the Go and Rust versions of `vibe_sat`:
a third algorithm, `--algorithm=dfs`, implementing a complete
DPLL-style depth-first search with boolean constraint propagation
(BCP) and the weighted `SelectVar` heuristic from `STAGE5.md`. Unlike
`hc`/`ws`, this search is complete — it can prove UNSAT, not just
report "not found yet".

## Correctness, checked several ways

Because this is the first algorithm in the project that makes a
*provable* claim (UNSAT, not just "didn't find a solution"), I
verified it more thoroughly than earlier stages:

- **Independent solution verification**: wrote a standalone script
  (not reusing any of vibe_sat's own code) that re-parses a CNF file
  and checks a written-out solution file against it literal by
  literal. Both languages' `dfs`-found SAT solutions passed on
  multiple benchmark files (100 variables, 430 clauses).
- **Broad correctness sweep**: ran `dfs` against 100 known-SAT
  (`uf20-91`) and 100 known-UNSAT (`uuf50-218`/`uuf75-325`) benchmark
  files; every verdict matched the benchmark's own labeling (0/200
  mismatches), each within a single-digit-second time budget.
- **Cross-language agreement**: ran both binaries' `dfs` on the same
  50 benchmark files and diffed their SAT/UNSAT verdicts — 0
  mismatches (the actual satisfying assignments found can differ
  between runs/languages, since branching order and tie-breaking are
  randomized, but the verdict and, when SAT, the *validity* of the
  returned assignment cannot).
- **A real UNSAT proof, not just "not found"**: distinguished this
  from Stages 2/4 by unit-testing that `Run`/`run` reports UNSAT
  (`TimedOut == false`) — a genuine proof of unsatisfiability — for
  both a trivial case (`x1` and `NOT x1`) and a non-trivial one (the
  pigeonhole principle, a classic hard-for-DPLL instance family, which
  I generated programmatically as a test helper in both languages).
  Also separately confirmed the time-limit path reports `UNKNOWN`
  (inconclusive, not a proof) rather than `UNSAT` when the clock — not
  the search — is what stopped it.
- **One test-methodology mistake worth flagging**: my first attempt at
  the independent Python solution-verifier appeared to find dozens of
  violated clauses. That turned out to be my own script picking a
  *different* CNF file than the one actually solved (`ls` sorts,
  Python's `glob.glob` doesn't), not a bug in the solver — re-running
  with the exact same file path in both halves showed the solution was
  correct all along. Flagging this so the "it looked broken, then
  wasn't" moment is visible rather than silently glossed over.

## BCP (`internal/dfs.BCP` / `dfs::bcp`)

Given a partial assignment with variable `i` just set, propagates unit
clauses to a fixed point: seeded with a queue containing `i`, for each
dequeued variable it looks up (via the Stage 2 occurrence lists) only
the clauses containing the literal that just became *false* because of
that assignment — the only clauses whose status could have changed.
Each such clause is classified in one pass over its literals:
satisfied (skip), a contradiction (0 unassigned literals, none true —
returns `Contra` immediately), a forced unit (exactly 1 unassigned
literal, none true — assign it and enqueue it), or still open (2+
unassigned — nothing to do). If the queue empties without a
contradiction, and every variable is now assigned, that's `Done`
(which, by construction, must mean every clause got satisfied along
the way); otherwise it's `OK`.

This is the same style of correctness argument used for the tricky
tautological-clause edge case in Stages 2/4's `flip`: I reasoned
explicitly (and wrote it into the code comments) about why touching
only the clauses containing a just-falsified literal is sufficient —
any clause that could become violated must have had one of its
literals flip false during *this* call, so it's guaranteed to be
re-examined at that exact moment.

## SelectVar (`internal/dfs.SelectVar` / `dfs::select_var`)

Implemented exactly as pseudocoded: for every not-yet-satisfied
clause, every unassigned variable in it gets `0.7^(n-2)` added to its
score (`n` = unassigned literals in that clause), and the
highest-scoring *unassigned* variable is picked, ties broken uniformly
at random. Two things worth noting:

- The pseudocode's own comment says `n >= 2` "due to BCP" — true for
  every call except the very first one, on the wholly unassigned root
  assignment, which is pushed directly without going through BCP
  first (per the stage's own pseudocode) and could see a shorter
  clause if the original problem contains one (`STAGE5.md`'s
  "Preprocessing" note explicitly says not to worry about that yet).
  The formula is well-defined either way (no division by zero, since
  it's a power, not a division), so no special-casing was needed.
- I added an explicit "only consider unassigned variables" filter when
  finding the max score. This isn't stated in the pseudocode, but is
  necessary: an already-assigned variable's score is always exactly 0
  (it can never be incremented, since increments only happen for
  variables the scoring loop finds *unassigned*), so without this
  filter it could tie with, and be incorrectly selected instead of, a
  genuinely unassigned variable.

## Depth-first search driver

Implemented as literally described: an explicit stack of full
`Assignment` copies (not an incremental trail/undo scheme — each
branch gets its own independent copy, matching the pseudocode's
`X' = X with x_i set to v` rather than a more traditional
backtracking-with-undo DPLL implementation, which the stage didn't ask
for). Per node: pop, `SelectVar`, then try `False` then `True`,
pushing `OK` branches back onto the stack and returning immediately on
`Done`. If the stack empties, `UNSAT`.

One degenerate case needed an explicit guard in both languages: a
0-variable problem. `SelectVar` would have nothing to select (every
DIMACS literal must reference a variable ≤ `NumVars`, so 0 variables
means every clause, if any exist, must be empty and therefore
unsatisfiable) — handled as a direct special case before the main loop
so it can't crash by branching on a nonexistent variable index.

## Time limit

Implemented per `STAGE5.md`'s suggestion: checked only every 4096
nodes (`num_nodes & 0xfff == 0`), not on every node, to avoid clock
overhead. On timeout, the search stops and reports `UNKNOWN` — an
inconclusive result, distinct from a genuine `UNSAT` proof, tracked
with a `TimedOut`/`timed_out` flag on the result. I applied the same
verbose-level gating (`>= 1`) to the final SAT/UNSAT/UNKNOWN print
that `hc`/`ws` already use, for consistency, since `STAGE5.md` doesn't
explicitly re-specify verbose behavior for `dfs` the way `STAGE2.md`
did for `hc`.

## Command line arguments

No new flag names, as specified. `--algorithm=dfs` takes no
`--alg-params` (rejected with an error if given) and an *optional*
`--time-limit-secs` (unlike `hc`/`ws`, which require at least one
stopping criterion — `dfs` doesn't need one, since it terminates on
its own once the search space is exhausted). `--help` was updated in
both languages to document this, including calling out that `dfs` can
report "UNSAT" where `hc`/`ws` can only ever report "UNKNOWN".

## Testing

- Go: `go vet`, `gofmt -l`, and `go test ./...` (7 packages) are all
  clean/passing. New `internal/dfs` tests cover `evaluateClause`'s
  four outcomes, `BCP`'s propagation chain/contradiction/OK paths,
  `SelectVar`'s weighting and unassigned-only filter, `Run` on
  satisfiable/unsatisfiable/pigeonhole/0-variable/time-limited inputs.
- Rust: `cargo fmt --check` and `cargo clippy --all-targets -- -D
  warnings` are clean; `cargo test` passes all 86 tests (the
  equivalent new tests to Go's).
- Manual: independent solution verification, 200-file SAT/UNSAT
  correctness sweep, 50-file Go/Rust cross-check, `--alg-params`
  rejection and `--help` content checked in both languages — all
  described above.

## Open questions / notes for you

- No language/toolchain version changes needed.
- The verbose-level conventions for `dfs`'s final status print
  (gated on `>= 1`, matching `hc`/`ws`) are my own choice for
  consistency, since `STAGE5.md` doesn't restate them the way
  `STAGE2.md` did. I did *not* invent any `dfs`-specific verbose >= 2/3
  behavior (e.g. periodic node-count progress), since nothing like
  that was requested and score-based concepts like "new best score"
  don't have an obvious DFS analogue.
- Performance note, not a problem: the `SelectVar` heuristic turned out
  to be quite effective in practice — e.g. it proved a 9-pigeons/8-holes
  instance (72 variables, a classically hard case for plain DPLL)
  UNSAT in about 0.15 seconds. Mentioning it since it's a nice
  real-world confirmation that the heuristic given in the stage is a
  reasonable one, not because anything needs changing.
