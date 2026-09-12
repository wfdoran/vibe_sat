//! Command line argument parsing for vibe_sat. Every argument supports
//! both a long form (`--long-name=value`) and a short form
//! (`-x value`); clap maps both forms onto the same field, so either
//! spelling may be used interchangeably.
//!
//! `--alg-params`/`-p` is unusual in that it accepts between one and
//! three values (`--alg-params <val1> [<val2> <val3>]`); clap's
//! `num_args` supports this directly.

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

    /// Solving algorithm to use (required; only "hc" is supported).
    #[arg(long = "algorithm", short = 'a')]
    pub algorithm: String,

    /// Where to write the solution, in DIMACS solution format.
    #[arg(long = "output", short = 'o')]
    pub output: Option<String>,

    /// Optional time limit, in seconds, for the search.
    #[arg(long = "time-limit-secs", short = 't')]
    pub time_limit_secs: Option<i64>,

    /// Algorithm-specific integer parameters (1 to 3 values). For
    /// --algorithm=hc, at most one parameter is accepted: the number
    /// of random restarts to perform.
    #[arg(long = "alg-params", short = 'p', num_args = 1..=3, allow_negative_numbers = true)]
    pub alg_params: Option<Vec<i64>>,
}

impl Args {
    /// Parses command line arguments from `argv` (an iterator whose
    /// first item is the program name, matching the shape of
    /// `std::env::args()`). Returns an error message describing the
    /// problem on failure (a missing required argument, an
    /// unrecognized flag, or an inconsistent combination of
    /// algorithm-specific arguments) instead of exiting the process,
    /// so callers can control how the error is reported.
    pub fn parse_from_args<I, T>(argv: I) -> Result<Args, String>
    where
        I: IntoIterator<Item = T>,
        T: Into<std::ffi::OsString> + Clone,
    {
        let args = Args::try_parse_from(argv).map_err(|e| e.to_string())?;
        validate(&args)?;
        Ok(args)
    }
}

/// Checks that the parsed [`Args`] are internally consistent for the
/// chosen algorithm: currently only `"hc"` is supported, and it
/// requires at least one of `--time-limit-secs` or a single
/// `--alg-params` value (the number of restarts), both of which must
/// be positive if given.
fn validate(args: &Args) -> Result<(), String> {
    match args.algorithm.as_str() {
        "hc" => {
            if let Some(params) = &args.alg_params {
                if params.len() > 1 {
                    return Err(
                        "for --algorithm=hc, --alg-params accepts at most one value (the number of starts)"
                            .to_string(),
                    );
                }
                if params[0] < 1 {
                    return Err(
                        "for --algorithm=hc, the number of starts given via --alg-params must be a positive integer"
                            .to_string(),
                    );
                }
            }
            if args.time_limit_secs.is_none() && args.alg_params.is_none() {
                return Err(
                    "for --algorithm=hc, either --time-limit-secs or --alg-params (number of starts) must be given"
                        .to_string(),
                );
            }
            if let Some(limit) = args.time_limit_secs
                && limit < 1
            {
                return Err("--time-limit-secs must be a positive integer".to_string());
            }
            Ok(())
        }
        other => Err(format!(
            "unsupported --algorithm value \"{other}\"; only \"hc\" is currently supported"
        )),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Returns a minimal valid argument list for algorithm "hc" that
    /// tests can append additional flags to.
    fn base_hc_args() -> Vec<&'static str> {
        vec![
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "--alg-params=10",
        ]
    }

    #[test]
    fn test_parse_long_form() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--verbose=2",
            "--algorithm=hc",
            "--alg-params=10",
        ])
        .expect("expected successful parse");
        assert_eq!(args.input, "problem.cnf");
        assert_eq!(args.verbose, 2);
        assert_eq!(args.algorithm, "hc");
    }

    #[test]
    fn test_parse_short_form() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "-i",
            "problem.cnf",
            "-v",
            "3",
            "-a",
            "hc",
            "-p",
            "10",
        ])
        .expect("expected successful parse");
        assert_eq!(args.input, "problem.cnf");
        assert_eq!(args.verbose, 3);
        assert_eq!(args.alg_params, Some(vec![10]));
    }

    #[test]
    fn test_parse_default_verbose() {
        let args = Args::parse_from_args(base_hc_args()).expect("expected successful parse");
        assert_eq!(args.verbose, 0);
    }

    #[test]
    fn test_parse_missing_input() {
        let result = Args::parse_from_args(["vibe_sat", "--algorithm=hc", "--alg-params=10"]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_missing_algorithm() {
        let result = Args::parse_from_args(["vibe_sat", "--input=problem.cnf", "--alg-params=10"]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_unsupported_algorithm() {
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=bogus",
            "--alg-params=10",
        ]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_unknown_flag() {
        let mut argv = base_hc_args();
        argv.push("--bogus=1");
        assert!(Args::parse_from_args(argv).is_err());
    }

    #[test]
    fn test_parse_hc_requires_starts_or_time_limit() {
        let result = Args::parse_from_args(["vibe_sat", "--input=problem.cnf", "--algorithm=hc"]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_hc_with_time_limit_only() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "--time-limit-secs=30",
        ])
        .expect("expected successful parse");
        assert_eq!(args.time_limit_secs, Some(30));
        assert_eq!(args.alg_params, None);
    }

    #[test]
    fn test_parse_hc_with_both_starts_and_time_limit() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "--alg-params=5",
            "--time-limit-secs=30",
        ])
        .expect("expected successful parse");
        assert_eq!(args.alg_params, Some(vec![5]));
        assert_eq!(args.time_limit_secs, Some(30));
    }

    #[test]
    fn test_parse_hc_rejects_multiple_alg_params() {
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "--alg-params",
            "5",
            "10",
        ]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_alg_params_space_separated_multiple_values() {
        // Tested against the tokenizer-level outcome (the raw parsed
        // Vec) regardless of the hc-specific restriction on length,
        // to confirm clap's num_args range collects up to three
        // values; the business-rule rejection is covered separately
        // above.
        let args = Args::try_parse_from([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "-p",
            "1",
            "2",
            "3",
        ])
        .expect("expected successful clap parse");
        assert_eq!(args.alg_params, Some(vec![1, 2, 3]));
    }

    #[test]
    fn test_parse_alg_params_allows_negative_number_value() {
        // Checked at the clap-tokenizing level: "-5" must be accepted
        // as alg_params's value rather than being mistaken for an
        // unrecognized flag. (validate() separately rejects a
        // negative number-of-starts on business-rule grounds, which
        // is covered by test_parse_hc_rejects_non_positive_alg_param
        // below.)
        let args = Args::try_parse_from([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "--time-limit-secs=10",
            "-p",
            "-5",
        ])
        .expect("expected successful clap parse");
        assert_eq!(args.alg_params, Some(vec![-5]));
    }

    #[test]
    fn test_parse_hc_rejects_non_positive_alg_param() {
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "--time-limit-secs=10",
            "-p",
            "-5",
        ]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_output_file() {
        let mut argv = base_hc_args();
        argv.push("--output=solution.txt");
        let args = Args::parse_from_args(argv).expect("expected successful parse");
        assert_eq!(args.output, Some("solution.txt".to_string()));
    }

    #[test]
    fn test_parse_negative_time_limit_rejected() {
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "--time-limit-secs=0",
        ]);
        assert!(result.is_err());
    }
}
