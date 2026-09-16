//! The `vibe_sat` library crate: every module the `vibe_sat` binary
//! (`src/main.rs`) implements, re-exported so this crate can also be
//! depended on by `benches/` (STAGE24.md).
//!
//! Rust's `cargo bench` (via the `criterion` crate, since stable Rust
//! has no built-in `#[bench]`) compiles each file under `benches/` as
//! its own separate binary, which can only reach this crate's code by
//! depending on it as a library -- there is no equivalent of Go's
//! `internal/` packages being directly importable by a `_test.go`/
//! `bench_test.go` file that already lives inside the same package.
//! This file exists purely to provide that library target; it adds no
//! behavior of its own; `main.rs` uses it the same way any other
//! dependent crate would (`use vibe_sat::cdcl;` in place of its old
//! `mod cdcl;`), so the compiled binary's behavior is unchanged.

pub mod assignment;
pub mod cdcl;
pub mod cliargs;
pub mod cnf;
pub mod dfs;
pub mod hillclimb;
pub mod occurrence;
pub mod preprocess;
pub mod solution;
