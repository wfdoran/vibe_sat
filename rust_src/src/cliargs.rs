//! Command line argument parsing for vibe_sat. Every argument supports
//! both a long form (`--long-name=value`) and a short form
//! (`-x value`); clap maps both forms onto the same field, so either
//! spelling may be used interchangeably.

use clap::Parser;

/// Command line arguments accepted by vibe_sat.
#[derive(Parser, Debug)]
#[command(name = "vibe_sat", about = "A command line SAT solver")]
pub struct Args {
    /// SAT CNF file to read in (required).
    #[arg(long = "input", short = 'i')]
    pub input: String,

    /// Verbose level. Default is 0.
    #[arg(long = "verbose", short = 'v', default_value_t = 0)]
    pub verbose: i32,
}

impl Args {
    /// Parses command line arguments from `argv` (an iterator whose
    /// first item is the program name, matching the shape of
    /// `std::env::args()`). Returns an error message describing the
    /// problem on failure (e.g. a missing required argument or an
    /// unrecognized flag) instead of exiting the process, so callers
    /// can control how the error is reported.
    pub fn parse_from_args<I, T>(argv: I) -> Result<Args, String>
    where
        I: IntoIterator<Item = T>,
        T: Into<std::ffi::OsString> + Clone,
    {
        Args::try_parse_from(argv).map_err(|e| e.to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_long_form() {
        let args = Args::parse_from_args(["vibe_sat", "--input=problem.cnf", "--verbose=2"])
            .expect("expected successful parse");
        assert_eq!(args.input, "problem.cnf");
        assert_eq!(args.verbose, 2);
    }

    #[test]
    fn test_parse_short_form() {
        let args = Args::parse_from_args(["vibe_sat", "-i", "problem.cnf", "-v", "3"])
            .expect("expected successful parse");
        assert_eq!(args.input, "problem.cnf");
        assert_eq!(args.verbose, 3);
    }

    #[test]
    fn test_parse_default_verbose() {
        let args = Args::parse_from_args(["vibe_sat", "--input=problem.cnf"])
            .expect("expected successful parse");
        assert_eq!(args.verbose, 0);
    }

    #[test]
    fn test_parse_missing_input() {
        let result = Args::parse_from_args(["vibe_sat", "--verbose=1"]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_unknown_flag() {
        let result = Args::parse_from_args(["vibe_sat", "--input=problem.cnf", "--bogus=1"]);
        assert!(result.is_err());
    }
}
