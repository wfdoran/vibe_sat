//! Command line argument parsing for vibe_sat. Every argument supports
//! both a long form (`--long-name=value`) and a short form
//! (`-x value`); clap maps both forms onto the same field, so either
//! spelling may be used interchangeably.
//!
//! `--alg-params`/`-p` is unusual in that it accepts between one and
//! four values (`--alg-params <val1> [<val2> <val3> <val4>]`); clap's
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

    #[arg(long = "alg-params", short = 'p', num_args = 1..=4, allow_negative_numbers = true)]
    alg_params: Option<Vec<String>>,

    #[arg(long = "no-preprocessing", short = 'x')]
    no_preprocessing: bool,

    #[arg(long = "num-threads", short = 'z', default_value_t = 1)]
    num_threads: usize,

    #[arg(long = "internal-params", short = 'c')]
    internal_params_path: Option<String>,
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

    /// Algorithm-specific integer parameters (1 to 4 values); see
    /// [`help_text`] for what each value means per algorithm. For
    /// `--algorithm=cdcl`, this holds the first (SelectVar), second
    /// (restart strategy, STAGE15.md), and fourth (phase strategy,
    /// STAGE43.md) values, in that order -- the third (a memory limit)
    /// is parsed separately into `memory_limit_bytes`, since it isn't
    /// a plain integer.
    ///
    /// Each element is `None` if and only if that position was given
    /// as `"_"` (STAGE44.md's underscore syntax, meaning "use this
    /// slot's own default" without needing to know or spell out what
    /// that default actually is) -- distinct from a position simply
    /// not being present at all (a shorter `Vec`, or `alg_params`
    /// itself being `None`), but every consumer treats the two
    /// identically: "fall through to whatever this slot's own default
    /// logic already does." This is what lets a caller reach a later
    /// slot (e.g. cdcl's fourth value) without committing to a real
    /// value for an earlier one they don't care about (e.g.
    /// `--alg-params _ _ _ 3` sets only the phase strategy).
    pub alg_params: Option<Vec<Option<i64>>>,

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

    /// `--internal-params`/`-c` (STAGE39.md): an explicit path to a
    /// `.vibe_sat.json`-shaped file of runtime-configurable internal
    /// tuning parameters (see the `params` module). If given, it is
    /// always used -- and a missing or invalid file at this path is
    /// always a hard error, never a silent fallback to the built-in
    /// defaults. If not given, `.vibe_sat.json` in the current
    /// directory is used if it exists, else the built-in defaults are
    /// used; see [`params::resolve`].
    pub internal_params_path: Option<String>,
}

/// Returns true if `argv` contains a `--help` or `-h` token anywhere.
/// This is checked by `main` before attempting to parse the rest of
/// the command line with [`Args::parse_from_args`], so that
/// `--help`/`-h` always works even when other required arguments are
/// missing or invalid.
pub fn wants_help(argv: &[String]) -> bool {
    argv.iter().any(|a| a == "--help" || a == "-h")
}

/// Returns true if `argv` contains a `--reset-internal-params` or `-q`
/// token anywhere (STAGE39.md). Checked by `main` before attempting to
/// parse the rest of the command line, exactly like [`wants_help`]: in
/// clap's derive, `Args::input`/`Args::algorithm` are required fields,
/// so `RawArgs::try_parse_from` refuses to return anything at all when
/// they're missing -- there is no later point (mirroring Go's
/// `validate()`, which runs after tokenizing) where a `ResetInternalParams`-
/// style flag could still bypass that requirement. Scanning `argv`
/// directly, before any parsing is attempted, sidesteps this
/// entirely, matching `--help`'s own precedent exactly.
pub fn wants_reset_internal_params(argv: &[String]) -> bool {
    argv.iter()
        .any(|a| a == "--reset-internal-params" || a == "-q")
}

/// Extracts `--internal-params`/`-c`'s value from `argv`, if present.
/// Used alongside [`wants_reset_internal_params`] to find
/// `--reset-internal-params`'s target file: that whole codepath
/// bypasses [`Args::parse_from_args`] (see [`wants_reset_internal_params`]'s
/// doc comment), so it never gets a chance to populate
/// `Args::internal_params_path` the normal way. Recognizes exactly the
/// `--internal-params=<path>`, `--internal-params <path>`, and
/// `-c <path>` forms clap itself accepts for this flag.
pub fn extract_internal_params_path(argv: &[String]) -> Option<String> {
    let mut iter = argv.iter();
    while let Some(a) = iter.next() {
        if let Some(v) = a.strip_prefix("--internal-params=") {
            return Some(v.to_string());
        }
        if a == "--internal-params" || a == "-c" {
            return iter.next().cloned();
        }
    }
    None
}

/// Extracts `--verbose`/`-v`'s value from `argv`, defaulting to 0 if
/// absent. Used alongside [`wants_reset_internal_params`] for the same
/// reason as [`extract_internal_params_path`]: `--reset-internal-params`
/// bypasses [`Args::parse_from_args`] entirely, so `Args::verbose`
/// itself is never populated on that path, even though
/// `--reset-internal-params`'s own "wrote default internal parameters
/// to ..." announcement is still meant to respect `--verbose >= 1`
/// like every other progress message.
pub fn extract_verbose(argv: &[String]) -> i32 {
    let mut iter = argv.iter();
    while let Some(a) = iter.next() {
        if let Some(v) = a.strip_prefix("--verbose=") {
            return v.parse().unwrap_or(0);
        }
        if a == "--verbose" || a == "-v" {
            return iter.next().and_then(|v| v.parse().ok()).unwrap_or(0);
        }
    }
    0
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

  --alg-params <val1> [<val2> <val3> <val4>], -p <val1> [<val2> <val3> <val4>]
        Algorithm-specific parameters (1 to 4 integer values); at
        least one of --alg-params or --time-limit-secs is required for
        "hc"/"ws". Every value is positional -- to set val2 you must
        also supply val1, to set val3 you must also supply val1 and
        val2, and so on -- but any value may be given as "_"
        (STAGE44.md) instead of a real number to mean "use this slot's
        own default," without needing to know or spell out what that
        default actually is: "--alg-params _ _ _ 3" (cdcl) sets only
        the phase strategy, leaving val1/val2/val3 all at their
        defaults. The meaning of each value depends on --algorithm:
          hc   val1 = number of random restarts to perform.
          ws   val1 = number of random restarts ("tries") to perform.
               val2 = max flips per try before giving up and starting
                      a new try (default 10000 if omitted).
               val3 = noise percent, 0-100: the chance of flipping a
                      uniformly random variable of the chosen
                      unsatisfied clause instead of the one that
                      breaks the fewest other clauses (default 50 if
                      omitted).
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
                 4 = Glucose's own data-driven policy (STAGE34.md):
                     restarts not on a fixed conflict-count schedule
                     but whenever the moving average LBD ("Literal
                     Block Distance", Audemard & Simon 2009) of the
                     last 50 learned clauses is close to or worse than
                     the all-time average LBD -- a sign the search has
                     drifted into learning less useful clauses than
                     its own history and is better off restarting.
                 5 = round-robin (STAGE21.md, --num-threads > 1 only;
                     STAGE44.md folded Glucose into the rotation):
                     worker 0 uses quadratic, worker 1 geometric,
                     worker 2 Luby, worker 3 Glucose, worker 4
                     quadratic again, and so on, so each of the four
                     strategies runs on close to an equal share of the
                     workers instead of every worker racing with the
                     same restart cadence. STAGE44.md numbers this 5
                     (moved from 4) so round-robin -- the one "meta"
                     choice above, not itself a schedule -- keeps the
                     highest numeral as the list of real strategies
                     grows.
               Optional; defaults to 2 (polynomial) with --num-threads=1
               (this project's own benchmark comparison,
               reports/REPORT15.md, found the polynomial schedule
               clearly ahead of no restarts and of Luby on this
               project's actual benchmark set, especially for proving
               UNSAT), or to 5 (round-robin) with --num-threads > 1
               (reports/REPORT20.md/REPORT21.md). An explicit val2,
               including 0, is always honored by every worker exactly
               as given, regardless of --num-threads. Note: val1 must
               be given to set val2, even if val1 is just the default
               (2) -- or use "_" (see above) to mean exactly that
               without needing to know it.
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
                     both be given to set val3 -- use "_" for either
                     (or both) if you want their defaults.
               val4 = phase-selection strategy (STAGE43.md): which
                     technique guesses a newly-decided variable's
                     polarity.
                 0 = phase saving (STAGE14.md): guess the polarity the
                     variable last held before becoming unassigned, or
                     False if it has never been assigned before.
                 1 = target phase (Chanseok Oh): guess the polarity
                     recorded at the search's deepest trail so far (the
                     most variables ever simultaneously assigned
                     without conflict), tracked separately from -- and
                     never overwritten by -- ordinary phase saving.
                 2 = periodic WalkSAT rephasing (reports/REPORT33.md
                     item 6): behaves like phase saving, except that
                     every so many restarts a short WalkSAT burst runs
                     over the current clause database and its result
                     overwrites the saved phase wholesale. In the rare
                     case that burst happens to be a complete
                     satisfying assignment on its own, that assignment
                     is reported as the search's own verdict directly.
                 3 = round-robin (--num-threads > 1 only): worker 0
                     uses phase saving, worker 1 target phase, worker
                     2 WalkSAT rephasing, worker 3 phase saving again,
                     and so on, diversifying strategy across the
                     portfolio the same way val2=5 diversifies restart
                     schedules. STAGE44.md deliberately keeps this
                     rotation's period (3) coprime with restart round-
                     robin's (4): since both cycle off the same worker
                     index, every worker up to the twelfth (lcm(3, 4))
                     gets a genuinely unique (restart, phase) pairing
                     before any repeat, rather than the two rotations
                     colliding on the same pattern every three workers.
               Optional; defaults to 0 (phase saving) with
               --num-threads=1, or to 3 (round-robin) with
               --num-threads > 1, matching val2's own default
               convention. Note: val1, val2, and val3 must all be
               given to set val4 -- use "_" (see above) for any of
               them you don't otherwise want to set, e.g.
               "--alg-params _ _ _ 3".

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
                 --alg-params val2=5 above for how each thread's
                 restart schedule is chosen.
        For every algorithm that honors it, a value larger than the
        machine's core count is allowed (oversubscription); a warning
        is printed at --verbose=1 or higher if --num-threads is at
        least twice the core count, in case that's unintentional.

  --internal-params=<filename>, -c <filename>
        Read runtime-configurable internal tuning parameters (restart
        base conflict counts, activity decay rates, the Glucose
        restart window/threshold, work-budget factors, and similar
        search-heuristic constants; STAGE39.md) from <filename>, a
        JSON file shaped like the one --reset-internal-params writes.
        Any field the file omits keeps its built-in default value. If
        this flag is not given, vibe_sat looks for a file named
        ".vibe_sat.json" in the current directory and uses it if
        present; otherwise every parameter uses its built-in default.
        Unlike the implicit ".vibe_sat.json" lookup, a file named
        explicitly via this flag that does not exist, or is not valid
        JSON, is always a hard error -- never a silent fallback to
        defaults.

  --reset-internal-params, -q
        Write the built-in default internal tuning parameters to
        ".vibe_sat.json" in the current directory (or to the file
        named by --internal-params/-c, if given), then exit
        immediately, without requiring --input or --algorithm. Intended
        as a starting point: run this once, then edit the resulting
        file's values before using --internal-params to try them.

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
    let mut alg_params: Vec<Option<i64>> = Vec::new();
    let mut memory_limit_bytes: Option<i64> = None;

    if let Some(values) = &raw.alg_params {
        if raw.algorithm == "cdcl" {
            if values.len() > 4 {
                return Err(
                    "for --algorithm=cdcl, --alg-params accepts at most four values (0-3 selecting which SelectVar heuristic to use, 0-5 selecting the restart strategy, an optional learned-clause database memory limit, and 0-3 selecting the phase-selection strategy)"
                        .to_string(),
                );
            }
            if let Some(first) = values.first() {
                alg_params.push(parse_alg_param_value(first)?);
            }
            if let Some(second) = values.get(1) {
                alg_params.push(parse_alg_param_value(second)?);
            }
            if let Some(third) = values.get(2) {
                // "_" (STAGE44.md) leaves memory_limit_bytes None,
                // exactly as if the third value had never been given
                // at all -- unbounded, the same default every other
                // unset slot falls back to.
                if third != "_" {
                    let limit = parse_byte_size(third)
                        .map_err(|e| format!("invalid memory limit for --alg-params: {e}"))?;
                    memory_limit_bytes = Some(limit);
                }
            }
            if let Some(fourth) = values.get(3) {
                // STAGE43.md's phase-selection strategy: a plain
                // integer, like the first/second values, appended to
                // alg_params as its third element (alg_params[2]) even
                // though it's the *fourth* raw --alg-params token --
                // memory_limit_bytes (the third token) is parsed into
                // its own field above, not into alg_params, so
                // alg_params itself only ever holds select_var/
                // restart/phase, never the memory limit.
                // STAGE44.md's underscore syntax is what actually
                // fixes the old wrinkle noted here (selecting a phase
                // strategy used to require also giving a *real* memory
                // limit as the third value): "--alg-params _ _ _ 3"
                // now reaches the fourth value while leaving the first
                // three all at their own defaults.
                alg_params.push(parse_alg_param_value(fourth)?);
            }
        } else {
            for value in values {
                alg_params.push(parse_alg_param_value(value)?);
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
        internal_params_path: raw.internal_params_path,
    })
}

/// Parses one plain-integer `--alg-params` value token: `"_"`
/// (STAGE44.md) returns `Ok(None)` -- "use this slot's own default,"
/// represented as `None` rather than any particular sentinel integer,
/// since every slot's actual default value differs (and, for cdcl's
/// restart/phase strategies, depends on `--num-threads`, which
/// `build_args` can't assume has already been parsed at this point
/// anyway) -- so the real default is resolved later, by whichever
/// consumer already resolves "not given at all." Anything else must
/// be a base-10 integer.
fn parse_alg_param_value(s: &str) -> Result<Option<i64>, String> {
    if s == "_" {
        return Ok(None);
    }
    s.parse::<i64>()
        .map(Some)
        .map_err(|_| format!("invalid value for --alg-params: \"{s}\""))
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
                if let Some(starts) = params[0]
                    && starts < 1
                {
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
                if let Some(tries) = params[0]
                    && tries < 1
                {
                    return Err(
                        "for --algorithm=ws, the number of tries given via --alg-params must be a positive integer"
                            .to_string(),
                    );
                }
                if let Some(&Some(max_flips)) = params.get(1)
                    && max_flips < 1
                {
                    return Err(
                        "for --algorithm=ws, the max-flips-per-try value given via --alg-params must be a positive integer"
                            .to_string(),
                    );
                }
                if let Some(&Some(noise)) = params.get(2)
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
                if let Some(variant) = params[0]
                    && variant != 0
                    && variant != 1
                {
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
            // The at-most-four-values check and the memory limit's
            // own syntax (plain integer, optionally with a
            // k/kb/m/mb/g/gb suffix) were already enforced in
            // build_args, since that's where the raw tokens are
            // available; only the remaining business rules (variant
            // is 0-3; restart strategy is 0-5; the limit, if given,
            // is positive; phase strategy is 0-3) are checked here --
            // each skipped (`None`, `"_"`) if the caller used
            // STAGE44.md's underscore syntax for that slot, since
            // there's nothing to range-check about "use the default."
            // STAGE13.md extends the first value's range from dfs's
            // 0/1 (Weighted/Fast) to also allow 2 (VSIDS) and 3 (LRB),
            // both cdcl-only. STAGE15.md adds the second value
            // (restart strategy: 0 = none, 1 = Luby, 2 = polynomial,
            // 3 = geometric). STAGE21.md adds a fourth restart-
            // strategy value (round-robin across the fixed-schedule
            // strategies by worker index, see
            // cdcl::RestartStrategy::RoundRobin) and STAGE34.md a
            // fifth (Glucose's own data-driven policy based on LBD,
            // see cdcl::RestartStrategy::Glucose); STAGE44.md swaps
            // which numeral is which (4 = Glucose, 5 = round-robin,
            // now spanning all four fixed/data-driven strategies) so
            // that round-robin -- the "meta" choice -- keeps the
            // highest number as the strategy list grows, rather than
            // sitting in the middle of it.
            // STAGE43.md adds a fourth alg_params element (phase
            // strategy: 0 = saving, 1 = target, 2 = WalkSAT rephasing,
            // 3 = round-robin across all three by worker index; see
            // cdcl::PhaseStrategy).
            if let Some(params) = &args.alg_params
                && let Some(variant) = params[0]
                && !(0..=3).contains(&variant)
            {
                return Err(
                    "for --algorithm=cdcl, the first --alg-params value must be 0, 1, 2, or 3 (selecting which SelectVar heuristic to use)"
                        .to_string(),
                );
            }
            if let Some(params) = &args.alg_params
                && let Some(&Some(restart)) = params.get(1)
                && !(0..=5).contains(&restart)
            {
                return Err(
                    "for --algorithm=cdcl, the second --alg-params value must be 0, 1, 2, 3, 4, or 5 (selecting the restart strategy)"
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
            if let Some(params) = &args.alg_params
                && let Some(&Some(phase)) = params.get(2)
                && !(0..=3).contains(&phase)
            {
                return Err(
                    "for --algorithm=cdcl, the fourth --alg-params value must be 0, 1, 2, or 3 (selecting the phase-selection strategy)"
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
        assert_eq!(args.alg_params, Some(vec![Some(10)]));
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
        assert_eq!(args.alg_params, Some(vec![Some(5)]));
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
        assert_eq!(args.alg_params, Some(vec![Some(5), Some(2000), Some(40)]));
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
            assert_eq!(args.alg_params, Some(vec![Some(variant)]));
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
            assert_eq!(args.alg_params, Some(vec![Some(variant)]));
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
    fn test_parse_cdcl_rejects_five_alg_params() {
        // STAGE43.md's phase strategy makes four the maximum
        // (SelectVar variant, restart strategy, memory limit, phase
        // strategy).
        let result = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=cdcl",
            "--alg-params",
            "0",
            "1",
            "100",
            "0",
            "5",
        ]);
        assert!(result.is_err());
    }

    #[test]
    fn test_parse_cdcl_accepts_phase_strategy() {
        // STAGE43.md's phase-selection strategy: a fourth --alg-params
        // value of 0-3.
        for phase in [0i64, 1i64, 2i64, 3i64] {
            let args = Args::parse_from_args([
                "vibe_sat",
                "--input=problem.cnf",
                "--algorithm=cdcl",
                "--alg-params",
                "2",
                "2",
                "100MB",
                &phase.to_string(),
            ])
            .unwrap_or_else(|e| panic!("Parse returned unexpected error for phase={phase}: {e}"));
            assert_eq!(args.alg_params, Some(vec![Some(2), Some(2), Some(phase)]));
            assert_eq!(args.memory_limit_bytes, Some(100 * 1024 * 1024));
        }
    }

    #[test]
    fn test_parse_cdcl_rejects_out_of_range_phase_strategy() {
        for phase in ["4", "-1"] {
            let result = Args::parse_from_args([
                "vibe_sat",
                "--input=problem.cnf",
                "--algorithm=cdcl",
                "--alg-params",
                "2",
                "2",
                "100MB",
                phase,
            ]);
            assert!(
                result.is_err(),
                "expected error for --alg-params 2 2 100MB {phase} with --algorithm=cdcl"
            );
        }
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
            assert_eq!(args.alg_params, Some(vec![Some(2), Some(restart)]));
        }
    }

    #[test]
    fn test_parse_cdcl_rejects_out_of_range_restart_strategy() {
        for restart in ["6", "-1"] {
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

    /// STAGE44.md moved Glucose's numeral from 5 to 4.
    #[test]
    fn test_parse_cdcl_accepts_glucose_restart_strategy() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=cdcl",
            "--alg-params",
            "2",
            "4",
        ])
        .expect("Parse with --alg-params 2 4 should succeed");
        assert_eq!(args.alg_params.as_deref(), Some(&[Some(2), Some(4)][..]));
    }

    /// STAGE44.md moved round-robin's numeral from 4 to 5, now
    /// spanning all four fixed/data-driven restart strategies.
    #[test]
    fn test_parse_cdcl_accepts_round_robin_restart_strategy() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=cdcl",
            "--alg-params",
            "2",
            "5",
        ])
        .expect("Parse with --alg-params 2 5 should succeed");
        assert_eq!(args.alg_params.as_deref(), Some(&[Some(2), Some(5)][..]));
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
        // four values (STAGE43.md); the business-rule rejection is
        // covered separately above.
        let raw = RawArgs::try_parse_from([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "-p",
            "1",
            "2",
            "3",
            "4",
        ])
        .expect("expected successful clap parse");
        assert_eq!(
            raw.alg_params,
            Some(vec![
                "1".to_string(),
                "2".to_string(),
                "3".to_string(),
                "4".to_string()
            ])
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
            "--internal-params",
            "-c",
            "--reset-internal-params",
            "-q",
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
    fn test_wants_reset_internal_params_long_form() {
        assert!(wants_reset_internal_params(&[
            "vibe_sat".to_string(),
            "--reset-internal-params".to_string(),
        ]));
    }

    #[test]
    fn test_wants_reset_internal_params_short_form() {
        assert!(wants_reset_internal_params(&[
            "vibe_sat".to_string(),
            "-q".to_string(),
        ]));
    }

    #[test]
    fn test_wants_reset_internal_params_false_without_flag() {
        assert!(!wants_reset_internal_params(&[
            "vibe_sat".to_string(),
            "--input=problem.cnf".to_string(),
        ]));
    }

    #[test]
    fn test_reset_internal_params_bypasses_missing_required_args() {
        // Unlike a normal parse, --reset-internal-params must not
        // require --input/--algorithm at all -- but that's enforced by
        // main() checking wants_reset_internal_params before ever
        // calling Args::parse_from_args, not by anything in this
        // module's own parsing, so there is nothing to assert about
        // Args::parse_from_args here; this test just documents that
        // wants_reset_internal_params itself doesn't care what else is
        // (or isn't) in argv.
        assert!(wants_reset_internal_params(&[
            "vibe_sat".to_string(),
            "--reset-internal-params".to_string(),
        ]));
    }

    #[test]
    fn test_extract_internal_params_path_long_form_equals() {
        assert_eq!(
            extract_internal_params_path(&[
                "vibe_sat".to_string(),
                "--internal-params=custom.json".to_string(),
            ]),
            Some("custom.json".to_string())
        );
    }

    #[test]
    fn test_extract_internal_params_path_long_form_space() {
        assert_eq!(
            extract_internal_params_path(&[
                "vibe_sat".to_string(),
                "--internal-params".to_string(),
                "custom.json".to_string(),
            ]),
            Some("custom.json".to_string())
        );
    }

    #[test]
    fn test_extract_internal_params_path_short_form() {
        assert_eq!(
            extract_internal_params_path(&[
                "vibe_sat".to_string(),
                "-c".to_string(),
                "custom.json".to_string(),
            ]),
            Some("custom.json".to_string())
        );
    }

    #[test]
    fn test_extract_internal_params_path_absent() {
        assert_eq!(
            extract_internal_params_path(&["vibe_sat".to_string()]),
            None
        );
    }

    #[test]
    fn test_extract_verbose_present() {
        assert_eq!(
            extract_verbose(&["vibe_sat".to_string(), "-v".to_string(), "2".to_string(),]),
            2
        );
        assert_eq!(
            extract_verbose(&["vibe_sat".to_string(), "--verbose=3".to_string()]),
            3
        );
    }

    #[test]
    fn test_extract_verbose_absent_defaults_to_zero() {
        assert_eq!(extract_verbose(&["vibe_sat".to_string()]), 0);
    }

    #[test]
    fn test_parse_internal_params_flag() {
        let mut argv = base_hc_args();
        argv.push("--internal-params=custom.json");
        let args = Args::parse_from_args(argv).expect("expected successful parse");
        assert_eq!(args.internal_params_path, Some("custom.json".to_string()));
    }

    #[test]
    fn test_parse_internal_params_flag_absent_is_none() {
        let args = Args::parse_from_args(base_hc_args()).expect("expected successful parse");
        assert_eq!(args.internal_params_path, None);
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
        assert_eq!(args.alg_params, Some(vec![Some(10)]));
    }

    /// Verifies STAGE44.md's underscore syntax: "_" in any
    /// `--alg-params` position produces a `None` element (not a parse
    /// error), leaving that slot unset for downstream default
    /// resolution, exactly as if it had never been given.
    #[test]
    fn test_parse_underscore_skips_slot() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=cdcl",
            "--alg-params",
            "_",
            "5",
        ])
        .expect("expected successful parse");
        assert_eq!(args.alg_params.as_deref(), Some(&[None, Some(5)][..]));
    }

    /// Verifies the concrete case STAGE43.md flagged and STAGE44.md
    /// fixes: reaching cdcl's fourth value (phase strategy) without
    /// committing to a real first/second/third value.
    #[test]
    fn test_parse_underscore_reaches_later_cdcl_slot() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=cdcl",
            "--alg-params",
            "_",
            "_",
            "_",
            "3",
        ])
        .expect("expected successful parse");
        assert_eq!(args.alg_params.as_deref(), Some(&[None, None, Some(3)][..]));
        assert_eq!(args.memory_limit_bytes, None);
    }

    /// Verifies that "_" in cdcl's third position leaves
    /// `memory_limit_bytes` `None` (unbounded), the same as that value
    /// never being given at all.
    #[test]
    fn test_parse_underscore_for_memory_limit() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=cdcl",
            "--alg-params",
            "2",
            "2",
            "_",
        ])
        .expect("expected successful parse");
        assert_eq!(args.memory_limit_bytes, None);
        assert_eq!(args.alg_params, Some(vec![Some(2), Some(2)]));
    }

    /// Verifies that "--alg-params _" still counts as "--alg-params
    /// was given" for hc's "at least one of --alg-params or
    /// --time-limit-secs" requirement, even though the resulting slot
    /// is `None`.
    #[test]
    fn test_parse_underscore_alone_satisfies_hc_requirement() {
        let args = Args::parse_from_args([
            "vibe_sat",
            "--input=problem.cnf",
            "--algorithm=hc",
            "--alg-params",
            "_",
        ])
        .expect("expected successful parse");
        assert_eq!(args.alg_params, Some(vec![None]));
    }
}
