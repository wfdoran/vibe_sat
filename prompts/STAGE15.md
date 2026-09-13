# Stage 15

## Restarts

For --agorithm=cdcl, implement restarts.  Let's do both the luby
sequence and simple geometric growth.

5. **Restarts** (Luby sequence, or simple geometric growth). Cheap to
   add once CDCL exists: periodically abandon the current decision
   stack and start over from the root, keeping every learned clause
   collected so far. This escapes runs of bad early decisions and is
   one of the best effort-to-payoff ratios in this whole list.
   References: Luby, Sinclair & Zuckerman, "Optimal Speedup of Las
   Vegas Algorithms," 1993; Gomes, Selman & Kautz, "Boosting
   Combinatorial Search Through Randomization," AAAI 1998.

## Internal Parameters

For the geometric growth, the restart sequence will be

  a * 1^2, a * 2^2, a * 3^2, a * 4^2, ...

where a is a constant.  I am assuming you will use node count for
restart statistic.  How to pick a?  If there is a standard value in
the litature, please use it.  If not, I might pick a to be about 1
second of work.  Of course this would depend the actual SAT problem.
Maybe use the uf250 and uuf250 problems as representative samples.

For luby the sequence is

  1 * b, 1 * b, 2 * b, 1 * b, 1 * b, 2 * b, 4 * b, ...

How to pick b?  Again, if there is standard value use it.  Otherwise
pick b to be about 1 second of work on the uf250/uuf250 problems.

Whatever you choose, make them internal parameters which we
optimize later.

## Command Line Parameter

We are going to add a third --arg-params with --algorithm=cdcl.

val1 = variable selection heuristic  (no change here)

val2 = restart strategy
  0 => no restarts
  1 => luby
  2 => geometric
Which one should be the default?  You choose.

val3 = learned-clause database memory limit (previous was val2)

User can provide none (all defaults), one (just selection heuristic),
two (selection heuristic and restart strategy), or all three.