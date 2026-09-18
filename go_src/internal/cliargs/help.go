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
                 5 = Glucose's own data-driven policy (STAGE34.md):
                     restarts not on a fixed conflict-count schedule
                     but whenever the moving average LBD ("Literal
                     Block Distance", Audemard & Simon 2009) of the
                     last 50 learned clauses is close to or worse than
                     the all-time average LBD -- a sign the search has
                     drifted into learning less useful clauses than
                     its own history and is better off restarting.
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
`
}
