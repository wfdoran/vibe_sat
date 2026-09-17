# benchcompare

A standing (not thrown-away) harness that runs the Go and Rust
`vibe_sat` binaries side by side across a chosen slice of `benchmark/`
and reports:

- whether the two binaries agree on every file's SAT/UNSAT/UNKNOWN
  verdict,
- whether every reported SAT solution independently checks out (a
  from-scratch DIMACS parser and clause checker, entirely separate from
  either binary's own code -- see `cnf.go`/`verify.go`),
- and how their wall-clock time compares.

This replaces the one-off Python/shell comparison scripts every stage
from `reports/REPORT8.md` through `reports/REPORT23.md` wrote, used
once, and discarded. See `reports/REPORT22.md` item 18 and
`reports/REPORT24.md` for the history and design rationale.

It lives here, under `util/`, rather than in `go_src`/`rust_src`,
per `prompts/PROMPT.md`'s Stage 18 carve-out for permanent side tooling
that the two solver implementations must not depend on.

## Quick start

From anywhere inside the repository (the project root is
auto-detected by walking up looking for `go_src/`, `rust_src/`, and
`benchmark/`):

```sh
cd util/benchcompare
go run . --dirs=uf250-1065,uuf250-1065 --algorithm=cdcl \
  --sample=20 --time-limit-secs=30
```

This builds both binaries (unless `--go-bin`/`--rust-bin` point at
existing ones), runs each on 20 sampled files from each of the two
named `benchmark/` subdirectories with `--algorithm=cdcl
--time-limit-secs=30`, and prints a summary table plus any file that
needs a human look (a cross-language mismatch, a failed independent
verification, or a crash).

## Flags

| Flag | Meaning |
|---|---|
| `--project-root` | Repository root (default: auto-detected) |
| `--go-bin` / `--rust-bin` | Pre-built binaries to use instead of building fresh ones |
| `--dirs` | Comma-separated `benchmark/` subdirectory names (at least one of `--dirs`/`--paths` is required) |
| `--paths` | Comma-separated literal directory paths (absolute, or relative to the current directory) -- for a local-only benchmark set that isn't under `benchmark/` at all, e.g. a SAT Competition download kept out of the repository entirely (see `reports/REPORT29.md`) |
| `--sample` | Sample at most this many files per `--dirs`/`--paths` entry (0 = all) |
| `--seed` | Seed for `--sample`'s file selection, for reproducibility |
| `--max-size-mb` | Exclude any `.cnf` file larger than this many megabytes (0 = no limit) -- see "A note on very large benchmark sets" below |
| `--algorithm` | `hc`, `ws`, `dfs`, or `cdcl` (required) |
| `--alg-params` | Passed through verbatim, e.g. `"2 4"` |
| `--time-limit-secs` | Passed through as `--time-limit-secs` |
| `--num-threads` | Passed through as `--num-threads` |
| `--no-preprocessing` | Passed through as `--no-preprocessing` |
| `--hard-timeout-secs` | Kills a single run after this long regardless of `--time-limit-secs` (default: time-limit+30s, or 120s with no time limit) -- a safety net so a hung or unexpectedly hard instance can never hang this harness itself |
| `--json` | Write full per-file results (including complete stdout/stderr) to this path |
| `--quiet` | Suppress the one-line-per-file progress output |

Every run is sequential (Go, then Rust, one file at a time) rather than
concurrent, deliberately: running both binaries at once on the same
machine would make their wall-clock times meaningless, since they'd be
competing for the same physical cores.

## Design notes

- Independently written CNF/solution parsers (`cnf.go`/`verify.go`):
  deliberately *not* importing `go_src`'s own `internal/cnf` (which,
  being an `internal` package in a different Go module, this tool
  structurally cannot import anyway) -- the whole point of an
  independent checker is that a bug shared between the solver's own
  parser and the checker's parser could hide a real mistake.
- Every run passes `--verbose=1` (to get the `SAT`/`UNSAT`/`UNKNOWN`
  verdict line) and a fresh `--output=<tmpfile>` (to get a solution
  file to verify whenever the verdict is `SAT`), regardless of what a
  human running `vibe_sat` directly would pass.
- `--sample`'s selection is seeded (`math/rand/v2`, `--seed`) so the
  same `(--seed, --sample, --dirs)` always selects the same files,
  even though the underlying directories may later grow.

## A note on very large benchmark sets

`reports/REPORT29.md` found that a single multi-hundred-megabyte,
multi-million-clause `.cnf` file (from the SAT Competition 2018 main
track set) can exhaust available memory well before
`--hard-timeout-secs` would ever fire -- unlike a merely slow run, an
OOM kill isn't something a timeout can protect against, since the
process is killed by the operating system, not by `vibe_sat`'s own
(or this tool's) logic. When pointing this tool at an unfamiliar
benchmark set of unknown size (via `--paths` especially, since
`benchmark/`'s own committed files are all known to be safe), set
`--max-size-mb` to something conservative first (a few tens of MB is a
reasonable starting point) rather than discovering the largest file
the hard way. Example, sweeping a local-only benchmark directory kept
outside the repository:

```sh
go run . --paths=../../sat_comp/2018 --algorithm=cdcl \
  --sample=30 --max-size-mb=20 --time-limit-secs=20
```
