# `vibe_sat` — Usage

`vibe_sat` is a command line SAT solver. This page is a nicely
formatted version of `vibe_sat --help` — a "man page" for the tool.
The Go and Rust builds accept exactly the same arguments and mean the
same thing by them; every example on this page works identically with
either binary.

```
Usage: vibe_sat --input=<filename> --algorithm=<string> [OPTIONS]
```

Every option has a long form (`--option=value`) and a short form
(`-x value`); they're interchangeable. Boolean flags (`--help`,
`--no-preprocessing`) take no value in either form.

## Required options

### `--input=<filename>`, `-i <filename>`

The DIMACS CNF file to read in and solve. Required.

### `--algorithm=<string>`, `-a <string>`

Which solving algorithm to use. Required. One of:

| Value | Algorithm | Complete? |
|---|---|---|
| `hc` | Basic hill-climbing local search | No — can only report `SAT` or `UNKNOWN` |
| `ws` | WalkSAT | No — can only report `SAT` or `UNKNOWN` |
| `dfs` | Depth-first search with unit propagation | Yes — can also prove `UNSAT` |
| `cdcl` | Conflict-driven clause learning (CDCL) | Yes — can also prove `UNSAT` |

`hc` and `ws` are incomplete local-search methods: given enough time
they'll usually find a solution to a satisfiable formula, but they can
never *prove* a formula has no solution — they just give up and report
`UNKNOWN`. `dfs` and `cdcl` are complete: given enough time (or no time
limit at all) they will either find a solution or prove none exists.
See [background.md](background.md) for how each algorithm actually
works.

## Common options

### `--verbose=<integer>`, `-v <integer>`

Verbosity level, default `0`. Level `1` prints the algorithm's own
progress banner, the final `SAT`/`UNSAT`/`UNKNOWN` verdict, and total
wall-clock time; higher levels (`2`, `3`) print additional
algorithm-specific detail (e.g. `hc`/`ws` print every time they find a
new best score, or get stuck and restart). Most of the examples on
[sample-runs.md](sample-runs.md) use `-v 1` — without it, `vibe_sat`
runs silently and only its exit code and (if requested) output file
tell you what happened.

### `--output=<filename>`, `-o <filename>`

Where to write a satisfying solution, in DIMACS solution format
(`s SATISFIABLE` followed by a `v <literals> 0` line). If omitted, the
solution is printed to the screen instead, but only when `--verbose`
is at least `1`; with neither `--output` nor `--verbose`, a found
solution is simply not written anywhere (only the exit code reflects
success).

### `--time-limit-secs=<integer>`, `-t <integer>`

Optional time limit for the search, in seconds. Required for `hc`/`ws`
unless `--alg-params` is given instead (they need *some* stopping
condition). Optional for `dfs`/`cdcl`, which otherwise run until they
find a solution or exhaust the search space — however long that takes.

### `--no-preprocessing`, `-x`

Skip preprocessing and hand the CNF file to the chosen algorithm
exactly as read. Preprocessing (unit propagation, pure literal
elimination, subsumption elimination, and bounded variable
elimination) runs by default, ahead of every algorithm, and can
significantly shrink the problem before the real search even starts.
See [background.md](background.md#preprocessing).

### `--num-threads=<integer>`, `-z <integer>`

Number of concurrent worker threads. Default `1` (single-threaded).
Honored by preprocessing and by every algorithm, though *how* each one
uses extra threads differs a lot — see
[background.md](background.md#multi-threaded-dfs-work-stealing) and
[background.md](background.md#multi-threaded-cdcl-shared-clause-database)
for the two very different parallelization strategies `dfs` and `cdcl`
use. A value larger than the machine's core count is allowed
(oversubscription); `vibe_sat` prints a warning at `--verbose=1` or
higher if you ask for at least twice the detected core count, in case
that's a typo rather than intentional.

| Algorithm | What extra threads do |
|---|---|
| preprocessing | Speeds up subsumption elimination only. Fully deterministic — the result is always identical to the single-threaded one, just faster. |
| `hc` / `ws` | Splits the restart/try count evenly across threads; the time limit, if any, is given to every thread in full rather than divided. |
| `dfs` | Genuine divide-and-conquer: the search tree is seeded with up to `num-threads` disjoint starting branches, explored via work-stealing between threads. |
| `cdcl` | A portfolio design: every thread independently searches the *entire* problem; the first to reach a verdict wins. Threads continuously share learned clauses with each other. |

### `--internal-params=<filename>`, `-c <filename>`

Path to a JSON file of runtime-configurable internal tuning constants
(restart-schedule bases/growth factors, LRB's alpha, the Glucose
restart policy's K/window size, learned-clause minimization's and
preprocessing's work-budget factors) — see
[internal-parameters.md](internal-parameters.md) for the complete
list and every value's default.

If not given, a file named `.vibe_sat.json` in the current directory
is used automatically if one exists; otherwise every parameter keeps
its built-in default. A parameter the file doesn't mention also keeps
its default — only the values you actually want to override need to
be present. Loading a config file (whether from this flag or the
implicit `.vibe_sat.json`) is announced at `--verbose=1` or higher.
An explicit `--internal-params` path that doesn't exist or can't be
parsed is an error (`vibe_sat` exits rather than silently falling
back to defaults).

### `--reset-internal-params`, `-q`

Write the file `--internal-params` would otherwise read from (the
given path, or `.vibe_sat.json` in the current directory if
`--internal-params` isn't given) populated with every internal
parameter's current built-in default, then exit — without requiring
`--input` or `--algorithm`. Intended as a starting point to hand-edit:
run this once, then change only the values you want different from
the defaults it just wrote.

```
$ vibe_sat --reset-internal-params
$ cat .vibe_sat.json
{
  "cdcl": {
    "lubyBaseConflicts": 100,
    ...
  },
  "preprocess": {
    "subsumptionWorkBudgetFactor": 64,
    "bveWorkBudgetFactor": 2000
  }
}
```

### `--help`, `-h`

Print the built-in help text and exit.

## `--alg-params`, `-p`

```
--alg-params <val1> [<val2> [<val3> [<val4>]]]
```

Up to four positional, algorithm-specific values. At least one of
`--alg-params` or `--time-limit-secs` is required for `hc`/`ws`. The
values are positional — to set the second value you must also supply
the first, even if it's just the default.

### `hc`

| Value | Meaning |
|---|---|
| `val1` | Number of random restarts to perform. |

### `ws`

| Value | Meaning |
|---|---|
| `val1` | Number of random restarts ("tries") to perform. |
| `val2` | Max flips per try before giving up and starting a new try. Default `10000`. |
| `val3` | Noise percent, `0`–`100`: chance of flipping a uniformly random variable of the chosen unsatisfied clause, instead of the one that breaks the fewest other clauses. Default `50`. |

### `dfs`

| Value | Meaning |
|---|---|
| `val1` | Which variable-selection heuristic to use. `0` (default): a weighted heuristic that looks at every not-yet-satisfied clause on every node — more expensive per node, but tends to keep the search tree smaller. `1`: a cheap static-order heuristic (lowest-numbered unassigned variable, no clause contents examined) — much faster per node, but tends to grow the tree. |

### `cdcl`

| Value | Meaning |
|---|---|
| `val1` | Variable-selection heuristic. `0`/`1`: same as `dfs`'s `val1`. `2` (default): VSIDS — scores variables by how often they've recently appeared while resolving a conflict, decayed over time. `3`: LRB — scores variables by how often they've recently *participated* in producing a learned clause, per conflict they've been assigned for. |
| `val2` | Restart strategy. `0`: no restarts. `1`: the Luby sequence. `2` (default with `--num-threads=1`): a quadratic "polynomial" growth sequence. `3`: a true geometric growth sequence (constant ratio between restart intervals). `4` (default with `--num-threads>1`): round-robin — each worker thread gets a different one of `{2, 3, 1}` in turn, so no two threads restart on the same cadence. `5`: Glucose's data-driven policy — restart when a moving average of recent learned-clause LBDs looks close to or worse than the all-time average, rather than on a fixed conflict-count schedule. |
| `val3` | Learned-clause database memory limit. Either a plain integer (bytes) or an integer with a `k`/`kb`/`m`/`mb`/`g`/`gb` suffix (case-insensitive), e.g. `100MB`. Unbounded by default. |
| `val4` | Phase-selection strategy: which technique to use for guessing a newly-decided variable's polarity. `0` (default with `--num-threads=1`): phase saving — guess the polarity the variable last held before becoming unassigned. `1`: target phase — guess the polarity recorded at the search's deepest trail so far, tracked separately from phase saving. `2`: periodic WalkSAT rephasing — like phase saving, except every so many restarts a short WalkSAT burst runs and its result overwrites the saved phase wholesale (if that burst happens to fully solve the problem on its own, that solution is reported directly). `3` (default with `--num-threads>1`): round-robin — each worker thread gets a different one of `{0, 1, 2}` in turn. |

`cdcl`'s defaults (VSIDS over LRB, the polynomial restart schedule over
Luby, plain phase saving) were chosen from real measurement on this
project's own benchmark set, not just the literature — see
[references.md](references.md) and `reports/REPORT13.md`/
`reports/REPORT15.md`/`reports/REPORT43.md` for the numbers. `val1`,
`val2`, and `val3` must all be given (even if `val3` is just an
otherwise-unwanted memory limit) to reach `val4` — every value here is
positional.

## Exit status

`vibe_sat` exits `0` on a normal run (regardless of whether the
formula turned out to be `SAT`, `UNSAT`, or `UNKNOWN` — those are
reported via stdout, not the exit code) and non-zero on an error (a
missing/malformed input file, invalid arguments, and so on).

See [sample-runs.md](sample-runs.md) for worked examples of most of
the above.
