# Stage 6

## Alternative SelectVar

Look through the literature and develop an alternative SelectVar
subroutine.  Maybe one which is much faster than the current one with
the trade off that the resulting DFS tree is larger.

## Command Line Parameters

No new parameters, but use --alg-params (or -p) to select
which SelectVar is used.
  -p 0 => the current SelectVar
  -p 1 => the new SelectVar
Default to 0 if none is given.

