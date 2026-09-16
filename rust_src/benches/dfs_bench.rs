//! STAGE24.md: permanent profiling-benchmark infrastructure for
//! `dfs`, mirroring `go_src/internal/dfs/bench_test.go` -- see this
//! crate's `Cargo.toml` for why this is a plain, manually-timed binary
//! rather than a criterion-based benchmark.
//!
//! Loads `benchmark/uuf175-753/uuf175-083.cnf`, the instance
//! REPORT19.md identified as the hardest file in that directory (a few
//! seconds single-threaded) while still small enough to actually
//! finish proving UNSAT within a reasonable time.
//!
//! Usage (from rust_src):
//!
//!     cargo bench --bench dfs_bench
//!
//! To profile with `perf` (available now that the target machine has
//! `kernel.perf_event_paranoid=1` set, per STAGE24.md):
//!
//!     cargo build --release --bench dfs_bench
//!     perf record --call-graph dwarf -- \
//!       $(find target/release/deps -maxdepth 1 -name 'dfs_bench-*' -executable | head -1)
//!     perf report

use std::hint::black_box;
use std::time::Instant;

use rand::SeedableRng;
use rand::rngs::StdRng;

use vibe_sat::cnf::read_dimacs;
use vibe_sat::dfs::{SelectVarVariant, run, run_parallel};
use vibe_sat::occurrence;

fn main() {
    let path = "../benchmark/uuf175-753/uuf175-083.cnf";
    let problem = read_dimacs(path, 0).unwrap_or_else(|e| panic!("failed to read {path}: {e}"));
    let lists = occurrence::build(&problem);

    {
        let start = Instant::now();
        let mut rng = StdRng::seed_from_u64(0x0102);
        let result = black_box(run(
            &problem,
            &lists,
            None,
            SelectVarVariant::Weighted,
            &mut rng,
            0,
        ));
        assert!(!result.satisfiable, "expected unsatisfiable");
        println!("BenchmarkRunHardSingleThreaded: {:?}", start.elapsed());
    }

    {
        let start = Instant::now();
        let mut rng = StdRng::seed_from_u64(0x0102);
        let result = black_box(run_parallel(
            &problem,
            &lists,
            None,
            SelectVarVariant::Weighted,
            8,
            &mut rng,
            0,
        ));
        assert!(!result.satisfiable, "expected unsatisfiable");
        println!("BenchmarkRunHardParallel8: {:?}", start.elapsed());
    }
}
