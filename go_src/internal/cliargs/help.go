package cliargs

// HelpText returns the full usage text printed when vibe_sat is
// invoked with --help/-h: a short synopsis followed by a description
// of every command line parameter vibe_sat currently understands.
//
// NOTE for future stages: whenever a new command line parameter is
// added, update this text to describe it too (per STAGE3.md).
func HelpText() string {
	return `Usage: vibe_sat --input=<filename> --algorithm=<string> [OPTIONS]

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
               (2) -- or use "_" (see below) to mean exactly that
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
                     technique decide uses to guess a newly-decided
                     variable's polarity.
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
        STAGE39.md: path to a JSON file of runtime-configurable
        internal tuning constants (restart-schedule bases/growth
        factors, LRB's alpha, RestartGlucose's K/window size, learned-
        clause minimization's and preprocessing's work-budget factors,
        WalkSAT rephasing's interval/flip budget -- see
        docs/internal-parameters.md for the complete list and every
        value's default). If not given, a file named
        .vibe_sat.json in the current directory is used if present;
        otherwise every parameter keeps its built-in default. A
        parameter the file doesn't mention also keeps its default --
        only the values you actually want to override need to be
        present. Loading a config file (whether from this flag or the
        implicit .vibe_sat.json) is announced at --verbose=1 or
        higher.

  --reset-internal-params, -q
        Write the file --internal-params would otherwise read from
        (the given path, or .vibe_sat.json in the current directory if
        --internal-params isn't given) populated with every internal
        parameter's current built-in default, then exit -- without
        requiring --input or --algorithm. Intended as a starting point
        to hand-edit: run this once, then change only the values you
        want different from the defaults it just wrote.

  --help, -h
        Print this help message and exit.
`
}
