//! vibe_sat is a command line SAT solver. The program reads a DIMACS
//! CNF file into memory and attempts to solve it with the selected
//! algorithm (a basic hill-climb local search, "hc"; WalkSAT, "ws";
//! a complete depth-first search, "dfs"; or conflict-driven clause
//! learning, "cdcl"), optionally printing progress and writing out a
//! satisfying assignment if one is found.

mod assignment;
mod cdcl;
mod cliargs;
mod cnf;
mod dfs;
mod hillclimb;
mod occurrence;
mod preprocess;
mod solution;

use assignment::Assignment;
use preprocess::PreprocessResult;

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

    let original_num_vars = problem.num_vars;
    let mut preresult: Option<PreprocessResult> = None;
    let mut problem = problem;
    if !args.no_preprocessing {
        let mut result = preprocess::run(&problem, args.verbose);
        if result.unsat {
            // Preprocessing alone already proves the original problem
            // has no solution, regardless of which algorithm was
            // requested; there is nothing left to search for.
            if args.verbose >= 1 {
                println!("UNSAT");
            }
            return ExitCode::from(0);
        }
        problem = result
            .problem
            .take()
            .expect("problem is Some when not unsat");
        preresult = Some(result);
    }

    let run_result = match args.algorithm.as_str() {
        "hc" => run_hill_climb(&problem, &preresult, original_num_vars, &args),
        "ws" => run_walksat(&problem, &preresult, original_num_vars, &args),
        "dfs" => run_dfs(&problem, &preresult, original_num_vars, &args),
        "cdcl" => run_cdcl(&problem, &preresult, original_num_vars, &args),
        _ => Ok(()),
    };
    if let Err(message) = run_result {
        println!("{message}");
        return ExitCode::from(1);
    }

    ExitCode::from(0)
}

/// Returns the assignment to write out for a found solution:
/// `assignment` as-is if preprocessing was skipped (`preresult` is
/// `None`), or reconstructed back to the original problem's variable
/// numbering otherwise (see [`PreprocessResult::reconstruct`]).
fn reconstructed_assignment(
    assignment: &Assignment,
    preresult: &Option<PreprocessResult>,
) -> Assignment {
    match preresult {
        Some(result) => result.reconstruct(assignment),
        None => assignment.clone(),
    }
}

/// Runs the hill-climbing algorithm against `problem` using the
/// restart/time limits and verbosity level given in `args`, then
/// reports and/or writes out the result. If `preresult` is `Some`,
/// the found assignment is reconstructed back to `original_num_vars`
/// variables before being written out.
fn run_hill_climb(
    problem: &cnf::Problem,
    preresult: &Option<PreprocessResult>,
    original_num_vars: usize,
    args: &Args,
) -> Result<(), String> {
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
        let assignment = reconstructed_assignment(&result.assignment, preresult);
        write_solution(&assignment, original_num_vars, args)?;
    }

    Ok(())
}

/// Runs the WalkSAT algorithm against `problem` using the
/// tries/max-flips/noise/time-limit settings and verbosity level
/// given in `args`, then reports and/or writes out the result. See
/// `cliargs::help_text` for how `args.alg_params` maps onto WalkSAT's
/// parameters. If `preresult` is `Some`, the found assignment is
/// reconstructed back to `original_num_vars` variables before being
/// written out.
fn run_walksat(
    problem: &cnf::Problem,
    preresult: &Option<PreprocessResult>,
    original_num_vars: usize,
    args: &Args,
) -> Result<(), String> {
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
        let assignment = reconstructed_assignment(&result.assignment, preresult);
        write_solution(&assignment, original_num_vars, args)?;
    }

    Ok(())
}

/// Runs the depth-first search algorithm against `problem` using the
/// optional time limit, `SelectVar` variant, and verbosity level given
/// in `args`, then writes out the result if a satisfying assignment
/// was found. Unlike `run_hill_climb`/`run_walksat`, a search that
/// exhausts its space without a time limit produces a proven UNSAT
/// verdict, not just "not found". Per STAGE6.md, `args.alg_params[0]`
/// (if given) selects the `SelectVar` variant: 0 (the default) for the
/// weighted heuristic from STAGE5.md, 1 for the cheaper static-order
/// heuristic from STAGE6.md. If `preresult` is `Some`, the found
/// assignment is reconstructed back to `original_num_vars` variables
/// before being written out.
fn run_dfs(
    problem: &cnf::Problem,
    preresult: &Option<PreprocessResult>,
    original_num_vars: usize,
    args: &Args,
) -> Result<(), String> {
    let lists = occurrence::build(problem);
    let mut rng = StdRng::from_rng(&mut rand::rng());

    let time_limit = args
        .time_limit_secs
        .map(|secs| Duration::from_secs(secs as u64));

    let variant = match args.alg_params.as_deref() {
        Some([1]) => dfs::SelectVarVariant::Fast,
        _ => dfs::SelectVarVariant::Weighted,
    };

    let result = dfs::run(problem, &lists, time_limit, variant, &mut rng, args.verbose);

    if result.satisfiable {
        let assignment = reconstructed_assignment(&result.assignment, preresult);
        write_solution(&assignment, original_num_vars, args)?;
    }

    Ok(())
}

/// Runs the conflict-driven clause learning algorithm (STAGE11.md)
/// against `problem` using the optional time limit, `SelectVar`
/// variant, and verbosity level given in `args`, then writes out the
/// result if a satisfying assignment was found. Like `run_dfs` (and
/// unlike `run_hill_climb`/`run_walksat`), a search that exhausts its
/// space without a time limit produces a proven UNSAT verdict, not
/// just "not found". `args.alg_params[0]` (if given) selects the
/// `SelectVar` variant: 0/1 match dfs's own Weighted/Fast heuristics,
/// and 2/3 (STAGE13.md) select cdcl's own VSIDS/LRB heuristics;
/// unlike `dfs`, `cdcl` defaults to VSIDS (`cdcl::SelectVarVariant::Vsids`)
/// if `--alg-params` is omitted entirely, per `cdcl`'s own module doc
/// comment. `args.alg_params[1]` (if given, via `--alg-params`'s
/// second value: STAGE15.md) selects the restart strategy: 0 disables
/// restarts, 1 selects the Luby sequence, 2 selects the quadratic
/// "polynomial" sequence, 3 selects the true geometric sequence;
/// `cdcl` defaults to the polynomial sequence
/// (`cdcl::RestartStrategy::Polynomial`) if not given.
/// `args.memory_limit_bytes` (if given, via `--alg-params`'s third
/// value: STAGE12.md, moved from the second value by STAGE15.md)
/// bounds the learned-clause database's estimated size, past which
/// the least active learned clauses are periodically deleted; `None`
/// leaves it unbounded, as before Stage 12. If `preresult` is `Some`,
/// the found assignment is reconstructed back to `original_num_vars`
/// variables before being written out.
fn run_cdcl(
    problem: &cnf::Problem,
    preresult: &Option<PreprocessResult>,
    original_num_vars: usize,
    args: &Args,
) -> Result<(), String> {
    let mut rng = StdRng::from_rng(&mut rand::rng());

    let time_limit = args
        .time_limit_secs
        .map(|secs| Duration::from_secs(secs as u64));

    // STAGE13.md leaves the default SelectVar variant for
    // --algorithm=cdcl up to this implementation. The literature
    // cited in cdcl's module doc comment reports LRB beating VSIDS on
    // SAT Competition instances, but this project's own benchmark
    // comparison (reports/REPORT13.md) found VSIDS clearly ahead of
    // both LRB and Weighted on this project's actual (uniform random
    // 3-SAT) benchmark set -- real measurement on the relevant
    // benchmarks wins out over a priori literature reasoning, so
    // VSIDS is the default here (unlike dfs, which keeps its own
    // Weighted default).
    let variant = match args.alg_params.as_deref() {
        Some([0, ..]) => cdcl::SelectVarVariant::Weighted,
        Some([1, ..]) => cdcl::SelectVarVariant::Fast,
        Some([2, ..]) => cdcl::SelectVarVariant::Vsids,
        Some([3, ..]) => cdcl::SelectVarVariant::Lrb,
        _ => cdcl::SelectVarVariant::Vsids,
    };

    // STAGE15.md leaves the default restart strategy up to this
    // implementation too; see cdcl::run's doc comment for why it's
    // RestartStrategy::Polynomial.
    let restart_strategy = match args.alg_params.as_deref() {
        Some([_, 0, ..]) => cdcl::RestartStrategy::None,
        Some([_, 1, ..]) => cdcl::RestartStrategy::Luby,
        Some([_, 2, ..]) => cdcl::RestartStrategy::Polynomial,
        Some([_, 3, ..]) => cdcl::RestartStrategy::Geometric,
        _ => cdcl::RestartStrategy::Polynomial,
    };

    let result = cdcl::run(
        problem,
        time_limit,
        variant,
        restart_strategy,
        args.memory_limit_bytes,
        &mut rng,
        args.verbose,
    );

    if result.satisfiable {
        let assignment = reconstructed_assignment(&result.assignment, preresult);
        write_solution(&assignment, original_num_vars, args)?;
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
