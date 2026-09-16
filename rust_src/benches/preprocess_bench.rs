//! STAGE24.md: permanent profiling-benchmark infrastructure for
//! preprocessing, mirroring `go_src/internal/preprocess/bench_test.go`.
//! See this crate's `Cargo.toml` for why this is a plain,
//! manually-timed binary rather than a criterion-based benchmark.
//!
//! Loads `benchmark/blocksworld/bw_large.c.cnf`, the exact instance
//! REPORT21.md's "Open questions" flagged as spending ~11 seconds in
//! preprocessing alone (confirmed there via `--no-preprocessing`: 11s
//! -> 34ms) -- REPORT22.md's item 2 ("parallelize preprocessing")
//! named this as real, measured evidence that this module, not any
//! solving algorithm, is the bottleneck on some real instances. This
//! benchmark exists so a future stage that pursues that item has a
//! profile to start from in both languages, not just Go's.
//!
//! Usage (from rust_src):
//!
//!     cargo bench --bench preprocess_bench

use std::time::Instant;

use vibe_sat::cnf::read_dimacs;
use vibe_sat::preprocess;

fn main() {
    let path = "../benchmark/blocksworld/bw_large.c.cnf";
    let problem = read_dimacs(path, 0).unwrap_or_else(|e| panic!("failed to read {path}: {e}"));

    let start = Instant::now();
    preprocess::run(&problem, 0);
    println!("BenchmarkRunHard: {:?}", start.elapsed());
}
