# Report: Stage 6

## Summary

Stage 6 is complete for both the Go and Rust versions of `vibe_sat`:
a second `SelectVar` heuristic for the `dfs` algorithm, selectable via
`--alg-params`/`-p` (0 = the existing weighted heuristic from Stage 5,
1 = the new one), defaulting to 0.

## The alternative heuristic chosen

I picked **static/lexicographic variable ordering**: always branch on
the lowest-numbered still-unassigned variable, without looking at any
clause contents at all. This is a standard baseline in the SAT
branching-heuristic literature (e.g. J. Marques-Silva, "The Impact of
Branching Heuristics in Propositional Satisfiability Algorithms,"
1999, which surveys and compares static ordering against dynamic
heuristics like DLIS, Jeroslow-Wang, and Böhm's heuristic) — it's
explicitly discussed there as the cheap extreme that smarter,
more-expensive heuristics are measured against, which matches
STAGE6.md's own framing ("much faster... trade-off that the resulting
DFS tree is larger") almost exactly. I considered a few other
standard options:

- **MOM / DLIS-style heuristics**: still require scanning clause
  contents (just with cheaper bookkeeping than the weighted scheme),
  so they don't demonstrate the same clean "looks at nothing" vs.
  "looks at everything" contrast the stage seems to be asking for.
- **VSIDS**: the classic modern CDCL heuristic, but it's fundamentally
  built around clause learning (it decays/bumps scores based on
  conflict clauses), which this solver doesn't have — it wouldn't be
  a natural fit without a much bigger change than this stage calls for.

Static ordering costs at most O(NumVars) per node with no clause
scanning whatsoever, versus the existing heuristic's O(total literals
in the formula) plus a `pow()` call per clause, on every single node.

## Empirical results (not just literature)

I measured both heuristics directly (using a small throwaway
instrumentation program that isn't part of the deliverable, deleted
after use, consistent with `PROMPT.md`'s "tmp directories... clean
them up when you are done") across benchmark files:

| Sample | Metric | Weighted (0) | Fast (1) |
|---|---|---|---|
| 40 files (`uf100-430` + `uuf50-218`) | total nodes | 7,217 | 524,696 (73×) |
| same | total wall time | 147.2 ms | 1432.2 ms |
| same | **time per node** | 20.4 µs | **2.7 µs (7.5× cheaper)** |
| 9-pigeon/8-hole instance (72 vars) | nodes | 40,319 | 378,343 (9.4×) |
| same | total wall time | 145.9 ms | 166.8 ms |
| same | time per node | 3.6 µs | 0.44 µs (8.2× cheaper) |

This confirms *both* halves of the trade-off STAGE6.md predicted: the
new heuristic really is several times cheaper per node, and it really
does grow the tree substantially — anywhere from ~9× to ~73× more
nodes on these instances. What it does *not* show is a net wall-clock
win on these particular benchmark sizes: the per-node savings didn't
fully offset the much larger tree for the smaller/easier instances,
though the gap narrowed considerably on the harder pigeonhole case
(166.8ms vs 145.9ms — only ~14% slower despite 9.4× more nodes),
suggesting the crossover point where "fast" wins on wall-clock time
outright is plausibly reachable on harder/larger instances than these
benchmarks provide. I'm reporting this honestly rather than cherry-picking:
STAGE6.md hedges with "maybe" and doesn't claim the cheaper heuristic
should win on total time, only that it's faster per decision at the
cost of a bigger tree — which is exactly what the numbers show.

## Implementation

Both languages added a `SelectVarVariant`/`SelectVarVariant` type
(`Weighted` = 0, the existing Stage 5 heuristic; `Fast` = 1, the new
one) threaded through `Run`/`run` as a new parameter, dispatching to
either the existing heuristic or a new `SelectVarFastPick`/
`select_var_fast_pick` function — a simple forward scan for the first
`Unassigned` variable, needing no random source at all (there's
nothing to break ties between, since the choice is always unique).

`main`/`main.rs` reads `--alg-params`'s first value (if given) to pick
the variant, defaulting to `Weighted` when `--alg-params` is omitted,
per STAGE6.md's "Default to 0 if none is given." CLI validation now
accepts at most one `--alg-params` value for `dfs`, restricted to `0`
or `1`; `--help` was updated to document this per the established
convention from earlier stages.

## Testing

- Go: `go vet`, `gofmt -l`, and `go test ./...` (7 packages) are all
  clean. New tests cover `SelectVarFastPick`'s basic behavior and its
  degenerate "nothing left unassigned" case, plus `Run` exercised with
  `SelectVarFast` against both a satisfiable formula (checking the
  returned assignment is actually valid) and an unsatisfiable
  pigeonhole instance (checking it's still a genuine proof, not a
  timeout) — mirroring the existing `SelectVarWeighted` test coverage
  from Stage 5.
- Rust: `cargo fmt --check` and `cargo clippy --all-targets -- -D
  warnings` are clean; `cargo test` passes all 92 tests (the
  equivalent new tests to Go's, including a `#[should_panic]` test for
  the degenerate case, since Rust's `select_var_fast_pick` panics
  there instead of returning a sentinel value the way Go's returns
  `-1` — a small, intentional per-language idiom difference, matching
  how `select_var`/`SelectVar` already handle that same situation in
  each language).
- Cross-language correctness check: ran both binaries with both `-p 0`
  and `-p 1` against 40 benchmark files (80 checks total) and
  confirmed the SAT/UNSAT/UNKNOWN verdict always matched between
  languages — 0 mismatches.

## Open questions / notes for you

- No language/toolchain version changes needed.
- The node-count/timing comparison numbers above came from a
  throwaway instrumentation program (not committed — it lived in
  `go_src/tmp_node_compare/`, used, and deleted, per the project's
  rules about temporary debugging directories). If you'd like this
  kind of node-count reporting available on an ongoing basis (e.g. via
  a verbose level), let me know and I can add it properly in a future
  stage — I didn't add it unprompted since it wasn't asked for.
