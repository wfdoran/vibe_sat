//! vibe_sat is a command line SAT solver. The program reads a DIMACS
//! CNF file into memory and attempts to solve it with the selected
//! algorithm (a basic hill-climb local search, "hc"; WalkSAT, "ws";
//! or a complete depth-first search, "dfs"), optionally printing
//! progress and writing out a satisfying assignment if one is found.

mod assignment;
mod cliargs;
mod cnf;
mod dfs;
mod hillclimb;
mod occurrence;
mod solution;

use assignment::Assignment;

use std::fs::File;
use std::io;
use std::process::ExitCode;
use std::time::Duration;

use cliargs::Args;
use rand::SeedableRng;
use rand::rngs::StdRng;

/// Program entry point. Parses command line arguments, reads in the
/// requested DIMACS CNF file, runs the selected solving algorithm,
/// reports and/or writes out the result, and returns a status code of
/// 0 on success (whether or not a solution was found) or non-zero if
/// any error occurs.
fn main() -> ExitCode {
    let argv: Vec<String> = std::env::args().collect();
    if cliargs::wants_help(&argv) {
        print!("{}", cliargs::help_text());
        return ExitCode::from(0);
    }

    let args = match Args::parse_from_args(argv) {
        Ok(args) => args,
        Err(message) => {
            println!("{message}");
            return ExitCode::from(1);
        }
    };

    let problem = match cnf::read_dimacs(&args.input, args.verbose) {
        Ok(problem) => problem,
        Err(message) => {
            println!("{message}");
            return ExitCode::from(1);
        }
    };

    if args.verbose >= 1 {
        cnf::print_summary(&problem);
    }

    let run_result = match args.algorithm.as_str() {
        "hc" => run_hill_climb(&problem, &args),
        "ws" => run_walksat(&problem, &args),
        "dfs" => run_dfs(&problem, &args),
        _ => Ok(()),
    };
    if let Err(message) = run_result {
        println!("{message}");
        return ExitCode::from(1);
    }

    ExitCode::from(0)
}

/// Runs the hill-climbing algorithm against `problem` using the
/// restart/time limits and verbosity level given in `args`, then
/// reports and/or writes out the result.
fn run_hill_climb(problem: &cnf::Problem, args: &Args) -> Result<(), String> {
    let lists = occurrence::build(problem);
    let mut rng = StdRng::from_rng(&mut rand::rng());

    let params = hillclimb::Params {
        num_starts: match &args.alg_params {
            Some(values) if values.len() == 1 => Some(values[0] as usize),
            _ => None,
        },
        time_limit: args
            .time_limit_secs
            .map(|secs| Duration::from_secs(secs as u64)),
    };

    let result = hillclimb::run(problem, &lists, params, &mut rng, args.verbose);

    if result.satisfiable {
        write_solution(&result.assignment, problem.num_vars, args)?;
    }

    Ok(())
}

/// Runs the WalkSAT algorithm against `problem` using the
/// tries/max-flips/noise/time-limit settings and verbosity level
/// given in `args`, then reports and/or writes out the result. See
/// `cliargs::help_text` for how `args.alg_params` maps onto WalkSAT's
/// parameters.
fn run_walksat(problem: &cnf::Problem, args: &Args) -> Result<(), String> {
    let lists = occurrence::build(problem);
    let mut rng = StdRng::from_rng(&mut rand::rng());

    let mut params = hillclimb::walksat::WalkSatParams::default();
    if let Some(values) = &args.alg_params {
        if let Some(&tries) = values.first() {
            params.num_tries = Some(tries as usize);
        }
        if let Some(&max_flips) = values.get(1) {
            params.max_flips_per_try = max_flips as usize;
        }
        if let Some(&noise) = values.get(2) {
            params.noise_percent = noise as u32;
        }
    }
    params.time_limit = args
        .time_limit_secs
        .map(|secs| Duration::from_secs(secs as u64));

    let result = hillclimb::walksat::run_walksat(problem, &lists, params, &mut rng, args.verbose);

    if result.satisfiable {
        write_solution(&result.assignment, problem.num_vars, args)?;
    }

    Ok(())
}

/// Runs the depth-first search algorithm against `problem` using the
/// optional time limit and verbosity level given in `args`, then
/// writes out the result if a satisfying assignment was found. Unlike
/// `run_hill_climb`/`run_walksat`, a search that exhausts its space
/// without a time limit produces a proven UNSAT verdict, not just
/// "not found".
fn run_dfs(problem: &cnf::Problem, args: &Args) -> Result<(), String> {
    let lists = occurrence::build(problem);
    let mut rng = StdRng::from_rng(&mut rand::rng());

    let time_limit = args
        .time_limit_secs
        .map(|secs| Duration::from_secs(secs as u64));

    let result = dfs::run(problem, &lists, time_limit, &mut rng, args.verbose);

    if result.satisfiable {
        write_solution(&result.assignment, problem.num_vars, args)?;
    }

    Ok(())
}

/// Writes out a satisfying assignment in DIMACS solution format: to
/// `args.output` if one was given, otherwise to stdout when
/// `args.verbose` is at least 1 (and nowhere, per STAGE2.md, if
/// neither condition holds).
fn write_solution(assignment: &Assignment, num_vars: usize, args: &Args) -> Result<(), String> {
    if let Some(output_path) = &args.output {
        let mut file = File::create(output_path)
            .map_err(|e| format!("could not create output file \"{output_path}\": {e}"))?;
        solution::write(&mut file, assignment, num_vars)
            .map_err(|e| format!("could not write output file \"{output_path}\": {e}"))?;
        return Ok(());
    }

    if args.verbose >= 1 {
        let mut stdout = io::stdout();
        let _ = solution::write(&mut stdout, assignment, num_vars);
    }

    Ok(())
}
