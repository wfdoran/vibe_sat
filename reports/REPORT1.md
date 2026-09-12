# Report: Stage 1

## Summary

Stage 1 is complete for both the Go and Rust versions of `vibe_sat`.
Both programs read a DIMACS CNF file into an in-memory data structure,
optionally print a short summary of the problem's size, and exit with
the status codes described in `STAGE1.md`. Both were tested against
every one of the 5,200 `.cnf` files in `benchmark/` and produce
identical (byte-for-byte) output.

## Data structure

Both languages use the same conceptual representation, expressed
idiomatically in each language:

- **Literal** — a signed integer whose magnitude is the 1-based
  variable index and whose sign is the polarity (DIMACS convention).
  Go: a defined type `Literal int32` with `Var()` and `IsNegative()`
  methods. Rust: a type alias `type Literal = i32` plus free functions
  `literal_var` / `literal_is_negative`, since plain `i32` can't carry
  inherent methods without a newtype wrapper (a newtype was judged
  unnecessary overhead for stage 1; this can be revisited later).
- **Clause** — a list of literals (`[]Literal` in Go, `Vec<Literal>`
  in Rust). An empty clause is valid and represents the (always false)
  empty clause.
- **Problem** — `NumVars` (the variable count declared on the `p cnf`
  line) plus `Clauses`. `NumClauses()`/`num_clauses()` and
  `NumLiterals()`/`num_literals()` are computed on demand rather than
  stored, so they can never go stale.

This is intentionally minimal: plain slices/vectors with no shared
mutable state, so it can later be handed out read-only to goroutines
or async tasks (or have per-worker copies made) once the solver itself
runs across multiple cores, without forcing a redesign.

## Problem ingest

`ReadDIMACS` (Go, in `internal/cnf/dimacs.go`) and `read_dimacs` (Rust,
in `src/cnf.rs`) implement the same parsing algorithm:

- Lines starting with `c` are comments and are skipped.
- Exactly one `p cnf <numVars> <numClauses>` header is required; a
  missing or duplicate header, or clause data before the header, is an
  error.
- Clause literals are read as a token stream after the header (a
  clause may legally span multiple lines and is only closed by a
  terminating `0`); a literal whose magnitude exceeds the declared
  variable count, a non-numeric token, or a final clause missing its
  terminating `0` is an error.
- The number of clauses actually parsed is checked against the count
  declared on the header line; a mismatch is an error.

**Issue found and resolved:** every file in `benchmark/uf20-91` (and,
it turned out, across the whole benchmark set) failed to parse at
first, with an "invalid literal `%`" error. These SATLIB benchmark
files end their clause section with a line containing only `%`,
followed by a trailing `0` and a blank line — a legacy convention from
the original DIMACS tooling, not part of the clause data itself. Both
parsers now treat a line starting with `%` as an end-of-clauses marker
and stop reading at that point, ignoring anything after it. This was
verified by running both binaries over all 5,200 benchmark files (all
parse successfully) and diffing their verbose output file-by-file
(zero mismatches).

Per the stage spec, at verbose level ≥ 1 the filename being read is
printed to stdout before the file is opened, and on any read error an
error message is printed to stdout and the program exits with status
1 (this applies regardless of verbose level). On success the program
exits with status 0.

## Problem summary printing

`PrintSummary` (Go) and `print_summary` (Rust) print, in this order:
number of variables, number of clauses, number of literals (the total
count of literal occurrences across all clauses, not distinct
literals). Both are called from `main` only when verbose ≥ 1.

## Command line arguments

Both programs accept:

- `--input=<filename>` / `-i <filename>` (required)
- `--verbose=<integer>` / `-v <integer>` (default 0)

**Go**: uses only the standard `flag` package (no external
dependency). Both the long and short flag names are registered against
the same struct field (e.g. `fs.StringVar(&args.InputFile, "input", ...)`
and `fs.StringVar(&args.InputFile, "i", ...)`), so either spelling
updates the same value with no extra bookkeeping.

**Rust**: uses the `clap` crate (v4, `derive` feature) — a dependency
was unavoidable per `PROMPT.md`, and clap is a general-purpose CLI
argument parser, not SAT-specific. `#[arg(long = "input", short = 'i')]`
gives the same long/short pairing as the Go side. `clap` also provides
free `--help` output as a side effect.

A missing `--input`/`-i` is reported as an error and the program exits
with status 1, consistent with the "errors go to stdout, exit
non-zero" convention used elsewhere. Note that this means errors are
printed to **stdout**, not stderr, in both languages — unusual for a
CLI tool, but that's what `STAGE1.md` specifies, so it's applied
uniformly to all error paths (not just CNF read errors) for
consistency.

## Project layout

```
go_src/
  go.mod                          module vibe_sat
  cmd/vibe_sat/main.go            entry point
  internal/cnf/                   Problem/Literal/Clause types, DIMACS reader, summary printer (+ tests)
  internal/cliargs/               command line argument parsing (+ tests)

rust_src/
  Cargo.toml                      package vibe_sat, depends on clap
  src/main.rs                     entry point
  src/cnf.rs                      Problem/Literal/Clause types, DIMACS reader, summary printer (+ tests)
  src/cliargs.rs                  command line argument parsing (+ tests)
```

These follow each language's standard conventions: Go's `cmd/<binary>/`
+ `internal/<package>/` layout, and Rust's `src/main.rs` plus sibling
modules.

## Testing

- Go: `go test ./...` — 24 tests across `internal/cnf` and
  `internal/cliargs`, all passing. `go vet ./...` is clean and the
  tree is `gofmt`-formatted.
- Rust: `cargo test` — 24 tests across `src/cnf.rs` and
  `src/cliargs.rs`, all passing. `cargo clippy --all-targets` reports
  no warnings and the tree is `cargo fmt`-formatted.
- Both binaries were additionally run against every `.cnf` file under
  `benchmark/` (5,200 files); both exit 0 on all of them, and their
  verbose (`-v 1`) output is identical between the two languages for
  every file.

One stylistic difference worth noting: Go's tests capture real stdout
via an `os.Pipe` swap to verify printed output, which is a common Go
idiom. The idiomatic Rust equivalent is to make the printing routines
generic over `io::Write` (`write_summary<W: Write>`, and an internal
`read_dimacs_announcing_to<W: Write>`) and test against an in-memory
buffer, with a thin wrapper (`print_summary`, `read_dimacs`) that
supplies real stdout. Same behavior, but no hidden/global
stdout-swapping in the Rust tests.

## Build/test environment note

Both toolchains' default caches (`$GOCACHE`, `~/.cargo/registry`) and
`~/.cargo` itself live under paths the sandbox treats as read-only in
this session. Builds and tests were run with `GOCACHE`/`GOMODCACHE`
and `CARGO_HOME`/`CARGO_TARGET_DIR` pointed at a writable scratch
directory instead. This is purely a sandbox artifact of this session
and should not affect normal `go build`/`go test` or `cargo
build`/`cargo test` invocations on your machine.

## Open questions / notes for you

- No changes needed to the installed toolchain versions (go1.26.5,
  cargo/rustc 1.98.0 both worked fine).
- The `%`-terminator handling described above was not specified in
  `STAGE1.md`; I added it because otherwise neither program could read
  any file in the provided `benchmark/` directory. Happy to revisit if
  you'd rather treat those files as malformed instead.
- No temporary directories were left behind in the project tree;
  scratch files used for testing lived outside the repo (Go's
  `t.TempDir()`, and a `$TMPDIR`-based helper in Rust's tests; manual
  build/smoke-test artifacts were written to the sandbox scratch
  directory, not into `go_src`, `rust_src`, or `benchmark`).
