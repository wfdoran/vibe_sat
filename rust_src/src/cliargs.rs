//! Command line argument parsing for vibe_sat. Every argument supports
//! both a long form (`--long-name=value`) and a short form
//! (`-x value`); clap maps both forms onto the same field, so either
//! spelling may be used interchangeably.
//!
//! `--alg-params`/`-p` is unusual in that it accepts between one and
//! three values (`--alg-params <val1> [<val2> <val3>]`); clap's
//! `num_args` supports this directly. `--algorithm=cdcl`'s third
//! value (STAGE12.md, a memory limit like "100MB"; shifted from the
//! second value to the third by STAGE15.md's restart strategy) isn't
//! a plain integer like every other `--alg-params` value in this
//! program, so clap collects the raw tokens as strings
//! ([`RawArgs::alg_params`]) and [`build_args`] parses each one
//! itself, depending on which algorithm was selected -- mirroring the
//! Go implementation's tokenize/buildArgs split for the same reason.

use clap::Parser;

/// The command line arguments clap parses directly, before any
/// algorithm-specific interpretation of `alg_params`'s raw string
/// tokens (see [`build_args`] and the module doc comment). Not
/// exposed outside this module; [`Args`] is the typed result callers
/// actually use.
#[derive(Parser, Debug)]
#[command(
    name = "vibe_sat",
    about = "A command line SAT solver",
    disable_help_flag = true
)]
struct RawArgs {
    #[arg(long = "input", short = 'i')]
    input: String,

    #[arg(long = "verbose", short = 'v', default_value_t = 0)]
    verbose: i32,

    #[arg(long = "algorithm", short = 'a')]
    algorithm: String,

    #[arg(long = "output", short = 'o')]
    output: Option<String>,

    #[arg(long = "time-limit-secs", short = 't')]
    time_limit_secs: Option<i64>,

    #[arg(long = "alg-params", short = 'p', num_args = 1..=3, allow_negative_numbers = true)]
    alg_params: Option<Vec<String>>,

    #[arg(long = "no-preprocessing", short = 'x')]
    no_preprocessing: bool,

    #[arg(long = "num-threads", short = 'z', default_value_t = 1)]
    num_threads: usize,
}

/// Command line arguments accepted by vibe_sat, after
/// algorithm-specific interpretation of the raw `--alg-params` tokens
/// (see [`RawArgs`] and [`build_args`]).
///
/// clap's own auto-generated `--help`/`-h` flag is disabled here
/// (`disable_help_flag`) because `--help`/`-h` is instead detected by
/// [`wants_help`] and handled by `main` before [`Args::parse_from_args`]
/// is ever called, so that it always works even when other required
/// arguments are missing.
#[derive(Debug)]
pub struct Args {
    /// SAT CNF file to read in (required).
    pub input: String,

    /// Verbose level. Default is 0.
    pub verbose: i32,

    /// Solving algorithm to use (required; "hc", "ws", "dfs", or "cdcl").
    pub algorithm: String,

    /// Where to write the solution, in DIMACS solution format.
    pub output: Option<String>,

    /// Optional time limit, in seconds, for the search.
    pub time_limit_secs: Option<i64>,

    /// Algorithm-specific integer parameters (1 to 3 values); see
    /// [`help_text`] for what each value means per algorithm. For
    /// `--algorithm=cdcl`, this holds the first (SelectVar) and second
    /// (restart strategy, STAGE15.md) values -- the third (a memory
    /// limit) is parsed separately into `memory_limit_bytes`, since it
    /// isn't a plain integer.
    pub alg_params: Option<Vec<i64>>,

    /// Skip preprocessing (STAGE8.md); default is to run it.
    pub no_preprocessing: bool,

    /// `--alg-params`/`-p`'s third value for `--algorithm=cdcl` only
    /// (STAGE12.md; shifted from the second value to the third by
    /// STAGE15.md, which inserted the restart strategy as the new
    /// second value): an optional learned-clause database memory
    /// limit, in bytes. Parsed by [`parse_byte_size`] from either a
    /// plain integer (bytes) or an integer immediately followed by
    /// "k"/"kb"/"m"/"mb"/"g"/"gb" (case-insensitive; see
    /// `--alg-params 0 1 100MB` in the help text). `None` if not
    /// given, in which case the database grows without bound, as it
    /// did before Stage 12.
    pub memory_limit_bytes: Option<i64>,

    /// `--num-threads`/`-z` (STAGE17.md): the number of concurrent
    /// worker threads to use, currently honored only by
    /// `--algorithm=hc`/`ws` (see `main`'s `run_hill_climb`/
    /// `run_walksat`). Defaults to 1 (single-threaded, matching every
    /// algorithm's behavior before Stage 17). Deliberately unbounded
    /// above -- a value larger than the machine's core count is
    /// allowed (oversubscription); `main` prints a one-line warning at
    /// `--verbose >= 1` if it looks large enough to be unintentional.
    pub num_threads: usize,
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
          cdcl Conflict-driven clause learning with non-chronological
               backtracking (STAGE11.md): on every conflict, derives
               and adds a new clause explaining it, then jumps
               directly back to the decision level where that clause
               is useful, instead of dfs's "try the other branch, one
               level up." Also proves UNSAT, like "dfs".

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
          cdcl val1 = which SelectVar heuristic to use:
                 0 = the weighted heuristic from STAGE5.md, same as
                     "dfs"'s val1=0.
                 1 = the cheap static-order heuristic from STAGE6.md,
                     same as "dfs"'s val1=1.
                 2 = VSIDS (STAGE13.md): scores each variable by how
                     often it has recently appeared while resolving a
                     conflict, decayed over time so recent conflicts
                     count more than old ones.
                 3 = LRB (STAGE13.md): scores each variable by how
                     often it has recently *participated* in producing
                     a learned clause, per conflict it has been
                     assigned for (a "learning rate").
               Optional; defaults to 2 (VSIDS) if --alg-params is not
               given -- unlike "dfs", which defaults to 0. STAGE13.md
               cites SAT Competition results where LRB outperforms
               VSIDS, but this project's own benchmark comparison
               (reports/REPORT13.md) found VSIDS clearly ahead of both
               LRB and the older structural heuristics on this
               project's actual (uniform random 3-SAT) benchmark set,
               so that measurement is what this default follows.
               val2 = restart strategy (STAGE15.md): periodically
                     abandons the current decision stack and starts
                     over from the root, keeping every learned clause
                     collected so far -- this escapes runs of bad
                     early decisions.
                 0 = no restarts.
                 1 = the Luby, Sinclair & Zuckerman sequence: restart
                     intervals of 1, 1, 2, 1, 1, 2, 4, ... (in
                     conflicts, times an internal scale constant).
                 2 = a quadratic "polynomial" growth sequence: restart
                     intervals of 1^2, 2^2, 3^2, 4^2, ... (in
                     conflicts, times an internal scale constant).
                 3 = the true geometric growth sequence: restart
                     intervals grow by a constant ratio each time (in
                     conflicts, times internal scale constants) --
                     this is what "geometric restarts" conventionally
                     means in the SAT literature (val2=2's sequence
                     was originally, incorrectly, called "geometric";
                     see reports/REPORT15.md).
                 4 = round-robin (STAGE21.md, --num-threads > 1 only):
                     worker 0 uses quadratic, worker 1 geometric,
                     worker 2 Luby, worker 3 quadratic again, and so
                     on, so each strategy runs on close to an equal
                     share of the workers instead of every worker
                     racing with the same restart cadence.
               Optional; defaults to 2 (polynomial) with --num-threads=1
               (this project's own benchmark comparison,
               reports/REPORT15.md, found the polynomial schedule
               clearly ahead of no restarts and of Luby on this
               project's actual benchmark set, especially for proving
               UNSAT), or to 4 (round-robin) with --num-threads > 1
               (reports/REPORT20.md/REPORT21.md). An explicit val2,
               including 0, is always honored by every worker exactly
               as given, regardless of --num-threads. Note: val1 must
               be given to set val2, even if val1 is just the default
               (2).
               val3 = an optional learned-clause database memory
                     limit (STAGE12.md; this was val2 before
                     STAGE15.md added the restart strategy above):
                     once the estimated size of the database exceeds
                     this, the least "active" learned clauses are
                     periodically deleted (MiniSat-style) to keep it
                     under control. Either a plain integer (a number
                     of bytes) or an integer immediately followed by
                     one of "k", "kb", "m", "mb", "g", or "gb"
                     (case-insensitive), e.g. "--alg-params 2 1 100MB".
                     Omitted by default, in which case the database
                     grows without bound. Note: val1 and val2 must
                     both be given to set val3, even if they are just
                     the defaults (2 and 1).

  --no-preprocessing, -x
        Skip preprocessing (STAGE8.md: unit propagation, pure literal
        elimination, subsumption elimination, and bounded variable
        elimination), and hand the CNF file to the chosen algorithm
        exactly as read. Preprocessing runs by default; this flag
        takes no value.

  --num-threads=<integer>, -z <integer>
        Number of concurrent worker threads to use (STAGE17.md,
        STAGE18.md, STAGE20.md/STAGE21.md, STAGE25.md). Default is 1
        (single-threaded). Honored by preprocessing and by
        "hc"/"ws"/"dfs"/"cdcl".
          preprocessing (STAGE25.md)
                 Speeds up subsumption elimination only (profiling
                 found it, not bounded variable elimination, dominates
                 preprocessing cost on real, clause-count-heavy
                 instances -- see reports/REPORT25.md); unit
                 propagation, pure literal elimination, and bounded
                 variable elimination remain single-threaded. Fully
                 deterministic regardless of thread count: the result
                 is always identical to the single-threaded one, just
                 (usually) faster.
          hc/ws  If --alg-params gives a restart/try count, it is
                 split as evenly as possible across the threads (each
                 doing ceil(count/num-threads)); --time-limit-secs, if
                 given, is handed to every thread in full rather than
                 divided, since the threads search concurrently.
          dfs    A genuine divide-and-conquer parallel search (not a
                 portfolio solver): the search tree is seeded with up
                 to num-threads disjoint starting branches, then
                 explored via work-stealing between threads. May use
                 fewer than num-threads threads if the tree has fewer
                 branches than that to hand out.
          cdcl   A portfolio, not divide-and-conquer, design (Option B
                 of reports/REPORT20.md): every thread independently
                 searches the *entire* original problem, so the first
                 thread to reach any verdict (SAT or UNSAT) is already
                 the answer for the whole run, and every other thread
                 stops. The only thing threads share is learned
                 clauses, continuously, through a lock-free per-thread
                 export buffer every other thread drains -- see
                 --alg-params val2=4 above for how each thread's
                 restart schedule is chosen.
        For every algorithm that honors it, a value larger than the
        machine's core count is allowed (oversubscription); a warning
        is printed at --verbose=1 or higher if --num-threads is at
        least twice the core count, in case that's unintentional.

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
        let raw = RawArgs::try_parse_from(argv).map_err(|e| e.to_string())?;
        let args = build_args(raw)?;
        validate(&args)?;
        Ok(args)
    }
}

/// Converts a [`RawArgs`] (clap's direct parse, with `alg_params` as
/// raw string tokens) into the typed [`Args`] callers use: every
/// `--alg-params` token is parsed as a plain integer, except
/// `--algorithm=cdcl`'s third token, which is parsed as a memory
/// size instead (see [`parse_byte_size`] and the module doc comment).
/// STAGE15.md's restart strategy is `--algorithm=cdcl`'s second
/// value, a plain integer appended to `alg_params` just like the
/// first.
fn build_args(raw: RawArgs) -> Result<Args, String> {
    let mut alg_params: Vec<i64> = Vec::new();
    let mut memory_limit_bytes: Option<i64> = None;

    if let Some(values) = &raw.alg_params {
        if raw.algorithm == "cdcl" {
            if values.len() > 3 {
                return Err(
                    "for --algorithm=cdcl, --alg-params accepts at most three values (0-3 selecting which SelectVar heuristic to use, 0-4 selecting the restart strategy, and an optional learned-clause database memory limit)"
                        .to_string(),
                );
            }
            if let Some(first) = values.first() {
                let p = first
                    .parse::<i64>()
                    .map_err(|_| format!("invalid value for --alg-params: \"{first}\""))?;
                alg_params.push(p);
            }
            if let Some(second) = values.get(1) {
                let p = second
                    .parse::<i64>()
                    .map_err(|_| format!("invalid value for --alg-params: \"{second}\""))?;
                alg_params.push(p);
            }
            if let Some(third) = values.get(2) {
                let limit = parse_byte_size(third)
                    .map_err(|e| format!("invalid memory limit for --alg-params: {e}"))?;
                memory_limit_bytes = Some(limit);
            }
        } else {
            for value in values {
                let p = value
                    .parse::<i64>()
                    .map_err(|_| format!("invalid value for --alg-params: \"{value}\""))?;
                alg_params.push(p);
            }
        }
    }

    Ok(Args {
        input: raw.input,
        verbose: raw.verbose,
        algorithm: raw.algorithm,
        output: raw.output,
        time_limit_secs: raw.time_limit_secs,
        alg_params: if alg_params.is_empty() {
            None
        } else {
            Some(alg_params)
        },
        no_preprocessing: raw.no_preprocessing,
        memory_limit_bytes,
        num_threads: raw.num_threads,
    })
}

/// Parses `s` as a byte count (STAGE12.md): either a plain
/// non-negative integer (a number of bytes), or such an integer
/// immediately followed by one of "k", "kb", "m", "mb", "g", or "gb"
/// (case-insensitive; e.g. "100MB", "100mb", and "100Mb" all parse
/// the same way), for kilobytes, megabytes, or gigabytes (each 1024
/// times the previous unit, not 1000).
fn parse_byte_size(s: &str) -> Result<i64, String> {
    let digit_count = s.chars().take_while(|c| c.is_ascii_digit()).count();
    if digit_count == 0 {
        return Err(format!("{s:?} does not start with a number"));
    }

    let (digits, unit) = s.split_at(digit_count);
    let n: i64 = digits
        .parse()
        .map_err(|_| format!("{s:?} is not a valid byte size"))?;

    let multiplier: i64 = match unit.to_lowercase().as_str() {
        "" => 1,
        "k" | "kb" => 1024,
        "m" | "mb" => 1024 * 1024,
        "g" | "gb" => 1024 * 1024 * 1024,
        _ => {
            return Err(format!(
                "{s:?} has an unrecognized unit {unit:?}; expected one of k, kb, m, mb, g, gb"
            ));
        }
    };

    Ok(n * multiplier)
}

/// Checks that the parsed [`Args`] are internally consistent for the
/// chosen algorithm: currently only `"hc"` is supported, and it
/// requires at least one of `--time-limit-secs` or a single
/// `--alg-params` value (the number of restarts), both of which must
/// be positive if given.
fn validate(args: &Args) -> Result<(), String> {
    if args.num_threads < 1 {
        return Err("--num-threads must be a positive integer".to_string());
    }

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

        "cdcl" => {
            // The at-most-three-values check and the memory limit's
            // own syntax (plain integer, optionally with a
            // k/kb/m/mb/g/gb suffix) were already enforced in
            // build_args, since that's where the raw tokens are
            // available; only the remaining business rules (variant
            // is 0-3; restart strategy is 0-4; the limit, if given,
            // is positive) are checked here. STAGE13.md extends the
            // first value's range from dfs's 0/1 (Weighted/Fast) to
            // also allow 2 (VSIDS) and 3 (LRB), both cdcl-only.
            // STAGE15.md adds the second value (restart strategy: 0 =
            // none, 1 = Luby, 2 = polynomial, 3 = geometric).
            // STAGE21.md adds a fourth restart-strategy value (4 =
            // round-robin across quadratic/geometric/Luby by worker
            // index, meaningful with --num-threads > 1; see
            // cdcl::RestartStrategy::RoundRobin).
            if let Some(params) = &args.alg_params
                && !(0..=3).contains(&params[0])
            {
                return Err(
                    "for --algorithm=cdcl, the first --alg-params value must be 0, 1, 2, or 3 (selecting which SelectVar heuristic to use)"
                        .to_string(),
                );
            }
            if let Some(params) = &args.alg_params
                && let Some(&restart) = params.get(1)
                && !(0..=4).contains(&restart)
            {
                return Err(
                    "for --algorithm=cdcl, the second --alg-params value must be 0, 1, 2, 3, or 4 (selecting the restart strategy)"
                        .to_string(),
                );
            }
            if let Some(limit) = args.memory_limit_bytes
                && limit < 1
            {
                return Err(
                    "for --algorithm=cdcl, the memory limit given via --alg-params must be a positive number of bytes"
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
            "unsupported --algorithm value \"{other}\"; only \"hc\", \"ws\", \"dfs\", and \"cdcl\" are currently supported"
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
    fn test_parse_cdcl_needs_no_stopping_criterion() {
        let args = Args::parse_from_args(["vibe_sat", "--input=problem.cnf", "--algorithm=cdcl"])
            .expect("expected successful parse");
        assert_eq!(args.time_limit_secs, None);
    }

    #[test]
    fn test_parse_cdcl_accepts_time_limit() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=cdcl",
            "--time-limit-secs=10",
        ])
        .expect("expected successful parse");
        assert_eq!(args.time_limit_secs, Some(10));
    }

    #[test]
    fn test_parse_cdcl_accepts_select_var_variant() {
        for variant in [0i64, 1i64, 2i64, 3i64] {
            let args = Args::parse_from_args([
                "vibe_sat",
                "--input=problem.cnf",
                "--algorithm=cdcl",
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
    fn test_parse_cdcl_rejects_out_of_range_alg_params() {
        for variant in ["5", "-1", "4"] {
            let result = Args::parse_from_args([
                "vibe_sat",
                "--input=problem.cnf",
                "--algorithm=cdcl",
                &format!("--alg-params={variant}"),
            ]);
            assert!(
                result.is_err(),
                "expected error for --alg-params={variant} with --algorithm=cdcl"
            );
        }
    }

    #[test]
    fn test_parse_cdcl_rejects_four_alg_params() {
        // STAGE15.md's restart strategy makes three the maximum
        // (SelectVar variant, restart strategy, memory limit).
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=cdcl",
            "--alg-params",
            "0",
            "1",
            "100",
            "5",
        ]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_cdcl_accepts_restart_strategy() {
        for restart in [0i64, 1i64, 2i64, 3i64] {
            let args = Args::parse_from_args([
                "vibe_sat",
                "--input=problem.cnf",
                "--algorithm=cdcl",
                "--alg-params",
                "2",
                &restart.to_string(),
            ])
            .unwrap_or_else(|e| {
                panic!("Parse returned unexpected error for --alg-params 2 {restart}: {e}")
            });
            assert_eq!(args.alg_params, Some(vec![2, restart]));
        }
    }

    #[test]
    fn test_parse_cdcl_rejects_out_of_range_restart_strategy() {
        for restart in ["5", "-1"] {
            let result = Args::parse_from_args([
                "vibe_sat",
                "--input=problem.cnf",
                "--algorithm=cdcl",
                "--alg-params",
                "2",
                restart,
            ]);
            assert!(
                result.is_err(),
                "expected error for --alg-params 2 {restart} with --algorithm=cdcl"
            );
        }
    }

    #[test]
    fn test_parse_cdcl_accepts_round_robin_restart_strategy() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=cdcl",
            "--alg-params",
            "2",
            "4",
        ])
        .expect("Parse with --alg-params 2 4 should succeed");
        assert_eq!(args.alg_params.as_deref(), Some(&[2, 4][..]));
    }

    #[test]
    fn test_parse_cdcl_accepts_memory_limit() {
        let cases: [(&str, i64); 9] = [
            ("100", 100),
            ("100k", 100 * 1024),
            ("100K", 100 * 1024),
            ("100kb", 100 * 1024),
            ("100KB", 100 * 1024),
            ("5m", 5 * 1024 * 1024),
            ("5MB", 5 * 1024 * 1024),
            ("2g", 2 * 1024 * 1024 * 1024),
            ("2Gb", 2 * 1024 * 1024 * 1024),
        ];
        for (token, want) in cases {
            let args = Args::parse_from_args([
                "vibe_sat",
                "--input=problem.cnf",
                "--algorithm=cdcl",
                "--alg-params",
                "0",
                "0",
                token,
            ])
            .unwrap_or_else(|e| {
                panic!("Parse returned unexpected error for --alg-params 0 0 {token}: {e}")
            });
            assert_eq!(
                args.memory_limit_bytes,
                Some(want),
                "--alg-params 0 0 {token}"
            );
        }
    }

    #[test]
    fn test_parse_cdcl_rejects_memory_limit_as_first_value() {
        // "100MB" alone is positionally val1 (the SelectVar
        // selector), not val3, and is not a valid SelectVar value.
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=cdcl",
            "--alg-params",
            "100MB",
        ]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_cdcl_rejects_malformed_memory_limit() {
        for bad in ["MB", "100XB", "100.5MB", ""] {
            let result = Args::parse_from_args([
                "vibe_sat",
                "--input=problem.cnf",
                "--algorithm=cdcl",
                "--alg-params",
                "0",
                "0",
                bad,
            ]);
            assert!(
                result.is_err(),
                "expected error for malformed memory limit {bad:?}"
            );
        }
    }

    #[test]
    fn test_parse_cdcl_rejects_non_positive_memory_limit() {
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=cdcl",
            "--alg-params",
            "0",
            "0",
            "0",
        ]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_cdcl_defaults_to_unbounded_memory() {
        let args = Args::parse_from_args(["vibe_sat", "--input=problem.cnf", "--algorithm=cdcl"])
            .expect("expected successful parse");
        assert_eq!(args.memory_limit_bytes, None);
    }

    #[test]
    fn test_parse_alg_params_space_separated_multiple_values() {
        // Tested against RawArgs directly (the raw clap-parsed
        // tokens) regardless of the hc-specific restriction on
        // length, to confirm clap's num_args range collects up to
        // three values; the business-rule rejection is covered
        // separately above.
        let raw = RawArgs::try_parse_from([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "-p",
            "1",
            "2",
            "3",
        ])
        .expect("expected successful clap parse");
        assert_eq!(
            raw.alg_params,
            Some(vec!["1".to_string(), "2".to_string(), "3".to_string()])
        );
    }

    #[test]
    fn test_parse_alg_params_allows_negative_number_value() {
        // Checked at the clap-tokenizing level: "-5" must be accepted
        // as alg_params's value rather than being mistaken for an
        // unrecognized flag. (validate() separately rejects a
        // negative number-of-starts on business-rule grounds, which
        // is covered by test_parse_hc_rejects_non_positive_alg_param
        // below.)
        let raw = RawArgs::try_parse_from([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "--time-limit-secs=10",
            "-p",
            "-5",
        ])
        .expect("expected successful clap parse");
        assert_eq!(raw.alg_params, Some(vec!["-5".to_string()]));
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
