//! STAGE24.md: permanent profiling-benchmark infrastructure for
//! `hc`/`ws`, mirroring `go_src/internal/hillclimb/bench_test.go`. See
//! this crate's `Cargo.toml` for why this is a plain, manually-timed
//! binary rather than a criterion-based benchmark.
//!
//! Loads a representative `uf100-430` instance and runs a fixed number
//! of restarts/tries (`BENCH_NUM_STARTS`) rather than relying on the
//! instance's own difficulty as a stopping point -- unlike dfs/cdcl,
//! hc/ws are incomplete searches (STAGE2.md/STAGE4.md) with no natural
//! "done" state to time against.
//!
//! Usage (from rust_src):
//!
//!     cargo bench --bench hillclimb_bench

use std::time::Instant;

use rand::SeedableRng;
use rand::rngs::StdRng;

use vibe_sat::cnf::read_dimacs;
use vibe_sat::hillclimb::walksat::{self, WalkSatParams};
use vibe_sat::hillclimb::{self, Params};
use vibe_sat::occurrence;

/// Deliberately generous: per REPORT4.md, plain hc rarely solves
/// uf100-430 within a modest flip budget at all, so this benchmark's
/// point is a fixed, reproducible amount of flip/score work, not
/// reaching a solution.
const BENCH_NUM_STARTS: usize = 200;

fn main() {
    let path = "../benchmark/uf100-430/uf100-01.cnf";
    let problem = read_dimacs(path, 0).unwrap_or_else(|e| panic!("failed to read {path}: {e}"));
    let lists = occurrence::build(&problem);

    {
        let start = Instant::now();
        let mut rng = StdRng::seed_from_u64(0x0102);
        let params = Params {
            num_starts: Some(BENCH_NUM_STARTS),
            ..Default::default()
        };
        hillclimb::run(&problem, &lists, params, &mut rng, 0);
        println!("BenchmarkHillClimbSingleThreaded: {:?}", start.elapsed());
    }

    {
        let start = Instant::now();
        let mut rng = StdRng::seed_from_u64(0x0102);
        let params = Params {
            num_starts: Some(BENCH_NUM_STARTS),
            ..Default::default()
        };
        hillclimb::run_parallel(&problem, &lists, params, 8, &mut rng, 0);
        println!("BenchmarkHillClimbParallel8: {:?}", start.elapsed());
    }

    {
        let start = Instant::now();
        let mut rng = StdRng::seed_from_u64(0x0102);
        let params = WalkSatParams {
            num_tries: Some(BENCH_NUM_STARTS),
            ..Default::default()
        };
        walksat::run_walksat(&problem, &lists, params, &mut rng, 0);
        println!("BenchmarkWalkSatSingleThreaded: {:?}", start.elapsed());
    }

    {
        let start = Instant::now();
        let mut rng = StdRng::seed_from_u64(0x0102);
        let params = WalkSatParams {
            num_tries: Some(BENCH_NUM_STARTS),
            ..Default::default()
        };
        walksat::run_walksat_parallel(&problem, &lists, params, 8, &mut rng, 0);
        println!("BenchmarkWalkSatParallel8: {:?}", start.elapsed());
    }
}
