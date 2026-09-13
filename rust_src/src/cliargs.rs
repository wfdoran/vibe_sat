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
///
/// clap's own auto-generated `--help`/`-h` flag is disabled here
/// (`disable_help_flag`) because `--help`/`-h` is instead detected by
/// [`wants_help`] and handled by `main` before [`Args::parse_from_args`]
/// is ever called, so that it always works even when other required
/// arguments are missing.
#[derive(Parser, Debug)]
#[command(
    name = "vibe_sat",
    about = "A command line SAT solver",
    disable_help_flag = true
)]
pub struct Args {
    /// SAT CNF file to read in (required).
    #[arg(long = "input", short = 'i')]
    pub input: String,

    /// Verbose level. Default is 0.
    #[arg(long = "verbose", short = 'v', default_value_t = 0)]
    pub verbose: i32,

    /// Solving algorithm to use (required; "hc", "ws", or "dfs").
    #[arg(long = "algorithm", short = 'a')]
    pub algorithm: String,

    /// Where to write the solution, in DIMACS solution format.
    #[arg(long = "output", short = 'o')]
    pub output: Option<String>,

    /// Optional time limit, in seconds, for the search.
    #[arg(long = "time-limit-secs", short = 't')]
    pub time_limit_secs: Option<i64>,

    /// Algorithm-specific integer parameters (1 to 3 values); see
    /// [`help_text`] for what each value means per algorithm.
    #[arg(long = "alg-params", short = 'p', num_args = 1..=3, allow_negative_numbers = true)]
    pub alg_params: Option<Vec<i64>>,

    /// Skip preprocessing (STAGE8.md); default is to run it.
    #[arg(long = "no-preprocessing", short = 'x')]
    pub no_preprocessing: bool,
}

/// Returns true if `argv` contains a `--help` or `-h` token anywhere.
/// This is checked by `main` before attempting to parse the rest of
/// the command line with [`Args::parse_from_args`], so that
/// `--help`/`-h` always works even when other required arguments are
/// missing or invalid.
pub fn wants_help(argv: &[String]) -> bool {
    argv.iter().any(|a| a == "--help" || a == "-h")
}

/// Returns the full usage text printed when vibe_sat is invoked with
/// `--help`/`-h`: a short synopsis followed by a description of every
/// command line parameter vibe_sat currently understands.
///
/// NOTE for future stages: whenever a new command line parameter is
/// added, update this text to describe it too (per STAGE3.md).
pub fn help_text() -> String {
    r#"Usage: vibe_sat --input=<filename> --algorithm=<string> [OPTIONS]

vibe_sat is a command line SAT solver.

Options:
  --input=<filename>, -i <filename>
        SAT CNF file to read in. Required.

  --verbose=<integer>, -v <integer>
        Verbosity level. Default is 0.

  --algorithm=<string>, -a <string>
        Solving algorithm to use. Required. Supported values:
          hc   A basic hill-climb local search (STAGE2.md).
          ws   WalkSAT (STAGE4.md), a more advanced local search that
               can escape local optima that trap "hc".
          dfs  A complete depth-first search using unit propagation
               (STAGE5.md). Unlike "hc"/"ws", dfs can prove UNSAT: it
               reports "UNSAT" (not "UNKNOWN") when the search space
               is exhausted without finding a solution.

  --output=<filename>, -o <filename>
        Where to write a satisfying solution, in DIMACS solution
        format. If not given, the solution is printed to the screen
        when --verbose is at least 1; otherwise it is not written
        anywhere.

  --time-limit-secs=<integer>, -t <integer>
        Optional time limit, in seconds, for the search. Required by
        "hc"/"ws" unless --alg-params is given instead; optional for
        "dfs" (which will otherwise run until it finds a solution or
        exhausts the search space, however long that takes).

  --alg-params <val1> [<val2> <val3>], -p <val1> [<val2> <val3>]
        Algorithm-specific parameters (1 to 3 integer values); at
        least one of --alg-params or --time-limit-secs is required for
        "hc"/"ws". The meaning of each value depends on --algorithm:
          hc   val1 = number of random restarts to perform.
          ws   val1 = number of random restarts ("tries") to perform.
               val2 = max flips per try before giving up and starting
                      a new try (default 10000 if omitted).
               val3 = noise percent, 0-100: the chance of flipping a
                      uniformly random variable of the chosen
                      unsatisfied clause instead of the one that
                      breaks the fewest other clauses (default 50 if
                      omitted).
               Values are positional: to set val2 or val3 you must
               also supply every value before it.
          dfs  val1 = which SelectVar heuristic to use (STAGE6.md):
                 0 = the default weighted heuristic from STAGE5.md
                     (looks at every not-yet-satisfied clause on every
                     node; more expensive, tends to keep the search
                     tree smaller).
                 1 = a cheap static-order heuristic that just picks
                     the lowest-numbered unassigned variable, looking
                     at no clause contents at all; much faster per
                     node, but tends to grow the search tree.
               Optional; defaults to 0 if --alg-params is not given.

  --no-preprocessing, -x
        Skip preprocessing (STAGE8.md: unit propagation, pure literal
        elimination, subsumption elimination, and bounded variable
        elimination), and hand the CNF file to the chosen algorithm
        exactly as read. Preprocessing runs by default; this flag
        takes no value.

  --help, -h
        Print this help message and exit.
"#
    .to_string()
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

        "ws" => {
            if let Some(params) = &args.alg_params {
                if params[0] < 1 {
                    return Err(
                        "for --algorithm=ws, the number of tries given via --alg-params must be a positive integer"
                            .to_string(),
                    );
                }
                if let Some(&max_flips) = params.get(1)
                    && max_flips < 1
                {
                    return Err(
                        "for --algorithm=ws, the max-flips-per-try value given via --alg-params must be a positive integer"
                            .to_string(),
                    );
                }
                if let Some(&noise) = params.get(2)
                    && !(0..=100).contains(&noise)
                {
                    return Err(
                        "for --algorithm=ws, the noise-percent value given via --alg-params must be between 0 and 100"
                            .to_string(),
                    );
                }
            }
            if args.time_limit_secs.is_none() && args.alg_params.is_none() {
                return Err(
                    "for --algorithm=ws, either --time-limit-secs or --alg-params (number of tries) must be given"
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

        "dfs" => {
            if let Some(params) = &args.alg_params {
                if params.len() > 1 {
                    return Err(
                        "for --algorithm=dfs, --alg-params accepts at most one value (0 or 1, selecting which SelectVar heuristic to use)"
                            .to_string(),
                    );
                }
                if params[0] != 0 && params[0] != 1 {
                    return Err(
                        "for --algorithm=dfs, the --alg-params value must be 0 or 1 (selecting which SelectVar heuristic to use)"
                            .to_string(),
                    );
                }
            }
            if let Some(limit) = args.time_limit_secs
                && limit < 1
            {
                return Err("--time-limit-secs must be a positive integer".to_string());
            }
            Ok(())
        }

        other => Err(format!(
            "unsupported --algorithm value \"{other}\"; only \"hc\", \"ws\", and \"dfs\" are currently supported"
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
    fn test_parse_ws_requires_tries_or_time_limit() {
        let result = Args::parse_from_args(["vibe_sat", "--input=problem.cnf", "--algorithm=ws"]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_ws_with_time_limit_only() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=ws",
            "--time-limit-secs=30",
        ])
        .expect("expected successful parse");
        assert_eq!(args.alg_params, None);
    }

    #[test]
    fn test_parse_ws_accepts_three_alg_params() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=ws",
            "--alg-params",
            "5",
            "2000",
            "40",
        ])
        .expect("expected successful parse");
        assert_eq!(args.alg_params, Some(vec![5, 2000, 40]));
    }

    #[test]
    fn test_parse_ws_rejects_non_positive_max_flips() {
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=ws",
            "--alg-params",
            "5",
            "0",
        ]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_ws_rejects_out_of_range_noise_percent() {
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=ws",
            "--alg-params",
            "5",
            "1000",
            "101",
        ]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_dfs_needs_no_stopping_criterion() {
        let args = Args::parse_from_args(["vibe_sat", "--input=problem.cnf", "--algorithm=dfs"])
            .expect("expected successful parse");
        assert_eq!(args.time_limit_secs, None);
    }

    #[test]
    fn test_parse_dfs_accepts_time_limit() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=dfs",
            "--time-limit-secs=10",
        ])
        .expect("expected successful parse");
        assert_eq!(args.time_limit_secs, Some(10));
    }

    #[test]
    fn test_parse_dfs_rejects_alg_params() {
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=dfs",
            "--alg-params=5",
        ]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_dfs_accepts_select_var_variant() {
        for variant in [0i64, 1i64] {
            let args = Args::parse_from_args([
                "vibe_sat",
                "--input=problem.cnf",
                "--algorithm=dfs",
                "--alg-params",
                &variant.to_string(),
            ])
            .unwrap_or_else(|e| {
                panic!("Parse returned unexpected error for --alg-params={variant}: {e}")
            });
            assert_eq!(args.alg_params, Some(vec![variant]));
        }
    }

    #[test]
    fn test_parse_dfs_rejects_multiple_alg_params() {
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=dfs",
            "--alg-params",
            "0",
            "1",
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

    #[test]
    fn test_wants_help_long_form() {
        assert!(wants_help(&["vibe_sat".to_string(), "--help".to_string()]));
    }

    #[test]
    fn test_wants_help_short_form() {
        assert!(wants_help(&["vibe_sat".to_string(), "-h".to_string()]));
    }

    #[test]
    fn test_wants_help_overrides_other_errors() {
        assert!(wants_help(&[
            "vibe_sat".to_string(),
            "--bogus=1".to_string(),
            "--help".to_string(),
        ]));
    }

    #[test]
    fn test_wants_help_false_without_flag() {
        assert!(!wants_help(&[
            "vibe_sat".to_string(),
            "--input=problem.cnf".to_string(),
        ]));
    }

    #[test]
    fn test_help_text_mentions_every_flag() {
        let text = help_text();
        for flag in [
            "--input",
            "-i",
            "--verbose",
            "-v",
            "--algorithm",
            "-a",
            "--output",
            "-o",
            "--time-limit-secs",
            "-t",
            "--alg-params",
            "-p",
            "--no-preprocessing",
            "-x",
            "--help",
            "-h",
        ] {
            assert!(text.contains(flag), "help_text() does not mention {flag}");
        }
    }

    #[test]
    fn test_parse_no_preprocessing_defaults_false() {
        let args = Args::parse_from_args(base_hc_args()).expect("expected successful parse");
        assert!(!args.no_preprocessing);
    }

    #[test]
    fn test_parse_no_preprocessing_long_form() {
        let mut argv = base_hc_args();
        argv.push("--no-preprocessing");
        let args = Args::parse_from_args(argv).expect("expected successful parse");
        assert!(args.no_preprocessing);
    }

    #[test]
    fn test_parse_no_preprocessing_short_form() {
        let mut argv = base_hc_args();
        argv.push("-x");
        let args = Args::parse_from_args(argv).expect("expected successful parse");
        assert!(args.no_preprocessing);
    }

    #[test]
    fn test_parse_no_preprocessing_does_not_consume_following_flag() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "--no-preprocessing",
            "--alg-params=10",
        ])
        .expect("expected successful parse");
        assert!(args.no_preprocessing);
        assert_eq!(args.alg_params, Some(vec![10]));
    }
}
