# Stage 2

## Hillclimb

In this stage, we are going to add the simplest SAT solving method
possible: a basic hillclimb.

  Score(X) = number of clauses satisfied by assignment X

  for num_starts or until time expired:
    X = random 0/1 assignment of variables
    while true:
      change = false
      R = random ordering the variables
      for i in R:
        X' = X with variable x_i flipped
	if score(X') > score(X):
	  X = X'
	  change = true
      if not change:
        break
     if Score(X) = num_clauses:
       report SAT
       write out solution X
       exit
  report UNKNOWN

  if verbose_level >= 1, print "hillclimb" along with the number of
    starts and/or time limit at the start.  At the end, print
    SAT or UNKNOWN.

  if verbose_level >= 2, print out the score every time it achieves
    a new best score across all starts.

  if verbose_level >= 3, print out the score every time it gets
    stuck and has to restart.

  Only check the time at each start.

## Partial Assignments

For this stage, the assignments will always be complete.  Each
variable will be set to either 0 or 1.  But in the future we will
likely be working with partial assignments.  So, make sure assignments
can take on three values: 0, 1, and 2 for unknown.  The value 2
is just a suggestion, use whatever marker for unknown works best.

## Tilt

This might be a good time to add a routine to precompute which clauses
each literal (variable and sign) is part of.  This will help in the
scoring of a variable flip and probably be used elsewhere in later
stages.

## Command Line Parameters

--algorithm=<string>  -a <string>
required
only allowed value "hc"

--output=<filename>   -o <filename>
where to write the solution in DIMACS solution format
if not given write it to the screen at verbose_level >= 1

--time-limit-secs=<integer>   -t <integer>
optional time limit in seconds

--alg-params <val1> [<val2> <val3>]      -p <val1> [<val2> <val3>]
algorithm parameters
for --algorithm=hc, at most one parameter which is the number of
starts.  For --algorithm=hc, either number of starts or time-limit
must be given.  Both can be given, and stop when either is hit.




