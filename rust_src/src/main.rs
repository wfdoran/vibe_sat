//! vibe_sat is a command line SAT solver. This stage of the program
//! reads a DIMACS CNF file into memory and attempts to solve it with
//! the selected algorithm (currently only a basic hill-climb local
//! search), optionally printing progress and writing out a satisfying
//! assignment if one is found.

mod assignment;
mod cliargs;
mod cnf;
mod hillclimb;
mod occurrence;
mod solution;

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
    let args = match Args::parse_from_args(std::env::args()) {
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

    if args.algorithm == "hc"
        && let Err(message) = run_hill_climb(&problem, &args)
    {
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
        write_solution(&result, problem.num_vars, args)?;
    }

    Ok(())
}

/// Writes out a satisfying assignment in DIMACS solution format: to
/// `args.output` if one was given, otherwise to stdout when
/// `args.verbose` is at least 1 (and nowhere, per STAGE2.md, if
/// neither condition holds).
fn write_solution(
    result: &hillclimb::SolveResult,
    num_vars: usize,
    args: &Args,
) -> Result<(), String> {
    if let Some(output_path) = &args.output {
        let mut file = File::create(output_path)
            .map_err(|e| format!("could not create output file \"{output_path}\": {e}"))?;
        solution::write(&mut file, &result.assignment, num_vars)
            .map_err(|e| format!("could not write output file \"{output_path}\": {e}"))?;
        return Ok(());
    }

    if args.verbose >= 1 {
        let mut stdout = io::stdout();
        let _ = solution::write(&mut stdout, &result.assignment, num_vars);
    }

    Ok(())
}
