//! vibe_sat is a command line SAT solver. This stage of the program
//! reads a DIMACS CNF file into memory and, when run with a verbose
//! level of 1 or higher, prints basic statistics about the problem
//! that was read.

mod cliargs;
mod cnf;

use cliargs::Args;
use std::process::ExitCode;

/// Program entry point. Parses command line arguments, reads in the
/// requested DIMACS CNF file, optionally prints summary statistics
/// about it, and returns a status code of 0 on success or non-zero if
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

    ExitCode::from(0)
}
