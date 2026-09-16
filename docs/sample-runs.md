# Sample runs

Worked examples using the files under `benchmark/`, from simplest to
most involved. Every command below was actually run (against the Go
build; the Rust build behaves identically) to make sure it works as
shown. Run them from the repository root; adjust the binary path if
yours lives somewhere else (`go_src/vibe_sat` or
`rust_src/target/release/vibe_sat` after building — see the top-level
[README](../README.md)).

See [background.md](background.md) for *why* each algorithm behaves
the way it does, and [usage.md](usage.md) for the full flag reference.

## The simplest possible run

```sh
vibe_sat -i benchmark/uf20-91/uf20-0100.cnf -a dfs -v 1
```

Reads a small (20-variable) satisfiable formula and solves it with
`dfs`, the complete depth-first search. `-v 1` is what makes it print
anything at all — without it, `vibe_sat` runs silently and just exits
`0`. Output looks like:

```
Reading CNF file: benchmark/uf20-91/uf20-0100.cnf
Number of variables: 20
Number of clauses: 91
Number of literals: 273
preprocess: vars 20->20 clauses 91->90 (units=0 pure=0 subsumed=1 eliminated=0)
dfs: select_var=0
SAT
s SATISFIABLE
v -1 2 3 -4 5 6 -7 8 9 10 -11 -12 13 14 15 -16 17 -18 19 20 0
wall clock time: 1.077412ms
```

The `preprocess:` line shows the built-in preprocessor already
shrank the formula slightly (one clause was subsumed) before `dfs`
ever ran. The `v` line is the satisfying assignment, one signed
literal per variable, in DIMACS solution format.

## Proving unsatisfiability

```sh
vibe_sat -i benchmark/uuf50-218/UUF50.218.1000/uuf50-0100.cnf -a dfs -v 1
```

Same idea, but this file (from SATLIB's `uuf50-218` set — every
instance in it is unsatisfiable by construction) has no solution.
`dfs` doesn't just give up; it exhaustively proves it:

```
...
dfs: select_var=0
UNSAT
wall clock time: 3.19989ms
```

`hc`/`ws` (below) can *never* print `UNSAT` — they're incomplete local
search, so the best they can ever report on a formula like this is
`UNKNOWN`. Only `dfs`/`cdcl` can prove unsatisfiability.

## Local search: `hc` and `ws`

```sh
vibe_sat -i benchmark/uf20-91/uf20-0100.cnf -a hc -p 50 -v 1
```

The basic hill-climb, given a budget of 50 random restarts
(`--alg-params`/`-p`'s one value for `hc`). `hc` gets stuck at local
optima easily; `ws` (WalkSAT) usually does much better on the same
kind of formula by occasionally accepting a worsening move:

```sh
vibe_sat -i benchmark/uf20-91/uf20-0100.cnf -a ws -p 20 5000 40 -v 1
```

`ws`'s three `--alg-params` values are positional: 20 tries, up to
5000 flips per try, 40% noise (the chance of a purely random flip
instead of the locally-best one). Add `-v 2` to either command to see
every time a run finds a new best score:

```sh
vibe_sat -i benchmark/uf100-430/uf100-01.cnf -a hc -p 200 -v 2
```

```
...
hillclimb: num_starts=200
new best score: 378/428
new best score: 379/428
new best score: 380/428
new best score: 383/428
...
```

## `cdcl`: modern conflict-driven search

```sh
vibe_sat -i benchmark/uuf250-1065/uuf250-01.cnf -a cdcl -v 1 -t 30
```

`cdcl` (conflict-driven clause learning) is what actually makes
250-variable, 1065-clause formulas like this one tractable — plain
`dfs` struggles far more at this size. `-t 30` caps the search at 30
seconds; drop it to let `cdcl` run until it reaches a definitive
answer.

`--alg-params`/`-p` picks `cdcl`'s heuristics explicitly:

```sh
vibe_sat -i benchmark/uuf250-1065/uuf250-01.cnf -a cdcl -p 2 2 1MB -v 1 -t 30
```

Here `val1=2` selects VSIDS (the default anyway), `val2=2` selects the
polynomial restart schedule (also the default), and `val3=1MB` caps
the learned-clause database at one megabyte, past which the
least-active learned clauses get periodically evicted.

## Multiple threads

```sh
vibe_sat -i benchmark/uuf175-753/uuf175-083.cnf -a dfs -z 4 -v 1
```

`dfs` splits the search tree itself across 4 worker threads
(divide-and-conquer, work-stealing between them when one runs dry).
`cdcl` parallelizes completely differently — every thread searches the
*whole* problem independently, sharing only what they learn:

```sh
vibe_sat -i benchmark/uuf175-753/uuf175-083.cnf -a cdcl -p 2 4 -z 4 -v 1 -t 10
```

`-p 2 4` explicitly asks for round-robin restarts (`cdcl`'s own
default the moment `-z` is more than 1, so this particular `-p` is
shown here just to make the choice explicit).

## Turning preprocessing off

```sh
vibe_sat -i benchmark/blocksworld/anomaly.cnf -a dfs -v 1 -x
```

`-x`/`--no-preprocessing` skips the built-in simplification pass
entirely. Compare this run's output (no `preprocess:` line at all) to
the same command without `-x` to see what preprocessing did — on some
larger instances in `benchmark/blocksworld/` in particular,
preprocessing alone accounts for the great majority of total run time
(see [background.md](background.md#preprocessing)).

## Writing the solution to a file

```sh
vibe_sat -i benchmark/uf20-91/uf20-0100.cnf -a dfs -o /tmp/solution.txt
```

No `-v` needed here — `-o` writes the DIMACS solution file regardless
of verbosity. `cat /tmp/solution.txt` afterward shows:

```
s SATISFIABLE
v -1 2 3 -4 5 6 -7 8 9 10 -11 -12 13 14 15 -16 17 -18 19 20 0
```

## Beyond uniform random 3-SAT

Every example above uses SATLIB's `uf*`/`uuf*` sets (uniform random
3-SAT). `benchmark/` also has a few *structured* problem families,
where preprocessing and clause sharing tend to matter more (see
[background.md](background.md)):

```sh
# Graph 3-coloring, encoded as SAT
vibe_sat -i benchmark/flat125-301/flat125-10.cnf -a dfs -v 1

# Blocks-world planning
vibe_sat -i benchmark/blocksworld/anomaly.cnf -a dfs -v 1

# Circuit fault analysis (DIMACS benchmark set)
vibe_sat -i benchmark/ssa/ssa2670-130.cnf -a cdcl -v 1 -t 10
```

The graph-coloring run above shows preprocessing shrinking a
375-variable, 1403-clause encoding down to 204 variables and 943
clauses before `dfs` ever starts — a much bigger effect than
preprocessing typically has on the random 3-SAT sets, where there's
little redundant structure for it to find.
