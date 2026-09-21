//! STAGE24.md: permanent version of REPORT23.md's *temporary*
//! Instant-based `cdcl` timing instrumentation (added, measured, and
//! reverted during that stage's own profiling work) -- mirrors
//! `go_src/internal/cdcl/bench_test.go`. See this crate's
//! `Cargo.toml` for why this is a plain, manually-timed binary rather
//! than a criterion-based benchmark.
//!
//! Loads `benchmark/uuf250-1065/uuf250-01.cnf`, the exact instance
//! REPORT21.md/REPORT23.md profiled (23.7s/21.3s single-threaded in
//! Go/Rust respectively), deliberately skipping Stage 8 preprocessing
//! -- negligible on this instance size, so there is nothing to gain
//! and an irrelevant extra frame to lose by routing through it here.
//!
//! Usage (from rust_src):
//!
//!     cargo bench --bench cdcl_bench
//!
//! To profile with `perf` (available now that the target machine has
//! `kernel.perf_event_paranoid=1` set, per STAGE24.md):
//!
//!     cargo build --release --bench cdcl_bench
//!     perf record --call-graph dwarf -- \
//!       $(find target/release/deps -maxdepth 1 -name 'cdcl_bench-*' -executable | head -1)
//!     perf report

use std::hint::black_box;
use std::time::Instant;

use rand::SeedableRng;
use rand::rngs::StdRng;

use vibe_sat::cdcl::{RestartStrategy, SelectVarVariant, run, run_parallel};
use vibe_sat::cnf::read_dimacs;
use vibe_sat::params;

fn main() {
    let path = "../benchmark/uuf250-1065/uuf250-01.cnf";
    let problem = read_dimacs(path, 0).unwrap_or_else(|e| panic!("failed to read {path}: {e}"));

    {
        let start = Instant::now();
        let mut rng = StdRng::seed_from_u64(0x0102);
        let result = black_box(run(
            &problem,
            None,
            SelectVarVariant::Vsids,
            RestartStrategy::Polynomial,
            None,
            &mut rng,
            0,
            &params::default().cdcl,
        ));
        assert!(!result.satisfiable, "expected unsatisfiable");
        println!("BenchmarkRunHardSingleThreaded: {:?}", start.elapsed());
    }

    {
        let start = Instant::now();
        let mut rng = StdRng::seed_from_u64(0x0102);
        let result = black_box(run_parallel(
            &problem,
            None,
            SelectVarVariant::Vsids,
            RestartStrategy::RoundRobin,
            None,
            8,
            &mut rng,
            0,
            &params::default().cdcl,
        ));
        assert!(!result.satisfiable, "expected unsatisfiable");
        println!("BenchmarkRunHardParallel8: {:?}", start.elapsed());
    }

    {
        // Deliberately oversubscribed, matching Go's BenchmarkRunHardParallel32 --
        // see STAGE23.md's request to look closely at clause-database
        // contention, where running well past the physical core count
        // is where any real contention cost should be most visible.
        let start = Instant::now();
        let mut rng = StdRng::seed_from_u64(0x0102);
        let result = black_box(run_parallel(
            &problem,
            None,
            SelectVarVariant::Vsids,
            RestartStrategy::RoundRobin,
            None,
            32,
            &mut rng,
            0,
            &params::default().cdcl,
        ));
        assert!(!result.satisfiable, "expected unsatisfiable");
        println!("BenchmarkRunHardParallel32: {:?}", start.elapsed());
    }
}
