# vibe_sat

`vibe_sat` is a command line SAT solver, implemented twice from the
same specification — once in Go, once in Rust — and developed side by
side, one stage at a time.

## Background and philosophy

The goal of this project (see `prompts/PROMPT.md`) is to build two
functionally identical SAT solvers, one in Go and one in Rust, in
order to directly compare the two languages' strengths and weaknesses
on the same problem: parsing input, representing a CNF formula,
implementing several different solving algorithms (local search and
complete search), building a small hand-rolled CLI, and managing
dependencies (Go's "avoid external packages" constraint vs. Rust's
"crates are inevitable" reality).

Development proceeds in stages, described by the `STAGEx.md` files in
`prompts/`: each stage adds one feature or algorithm at a time,
and both language versions must keep accepting the exact same command
line arguments and producing equivalent results for the same input,
even when the two implementations' internal code organization differs
to fit each language's own idioms (for example, Go's package-scoped
privacy vs. Rust's module-tree privacy shape how code is split across
files for the same feature). `reports/REPORTx.md` documents what was
done, any issues encountered, and any judgment calls made, for each
corresponding stage.

## Documentation

- [**Usage**](docs/usage.md) — the full command line reference, a
  nicely formatted version of `vibe_sat --help`.
- [**Sample runs**](docs/sample-runs.md) — worked examples using files
  from `benchmark/`, from the simplest possible invocation up through
  multi-threading and preprocessing.
- [**Background**](docs/background.md) — how each algorithm actually
  works (DFS/DPLL, CDCL, WalkSAT, preprocessing, restarts,
  non-chronological backtracking, the two very different
  multi-threading designs `dfs` and `cdcl` use), this project's
  testing methodology, and what the Go-vs-Rust comparison actually
  found.
- [**References**](docs/references.md) — the papers that shaped
  specific design decisions, and which report each one is discussed
  in.

## Building both `vibe_sat`s

### Go

Requires Go 1.26.5 or later.

```sh
cd go_src
go build -o vibe_sat ./cmd/vibe_sat
./vibe_sat --help
```

Run the test suite with:

```sh
cd go_src
go test ./...
```

### Rust

Requires Rust/Cargo 1.98.0 or later.

```sh
cd rust_src
cargo build --release
./target/release/vibe_sat --help
```

Run the test suite with:

```sh
cd rust_src
cargo test
```

## Project layout

```
/prompts     PROMPT.md and STAGEx.md files describing each development stage
/go_src      Go source code
/rust_src    Rust source code
/reports     REPORTx.md write-ups, one per stage
/benchmark   sample DIMACS CNF problems used for testing
/docs        usage reference, sample runs, background, and references (see below)
```

## Credit

All of the code in repo was written by Sonnet 5.  This is my first
attempt at a completely vibe code project.

The sample benchmark problems were taken from the
[SATLIB Benchmark Problems](https://www.cs.ubc.ca/~hoos/SATLIB/benchm.html) page.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for the
full license text.

## Warranty

This software is provided "as is", without warranty of any kind, express or
implied. See the LICENSE file for the complete warranty disclaimer.


