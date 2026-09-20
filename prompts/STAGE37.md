# Stage 37

I want to do this next.

5. **Smoke test in CI, your new idea.** Genuinely cheap: a handful of small
   `benchmark/` files run through the actual CLI (both languages, a couple
   of flag combinations) added to the existing GitHub Actions workflow
   (Stage 28), asserting the process exits cleanly and prints the expected
   verdict. This is real end-to-end confirmation that a change hasn't
   silently broken something the unit tests don't cover (e.g. an argument-
   parsing regression, a build that compiles but panics on real input) —
   near-zero effort given the CI infrastructure and benchmark files already
   exist, and it closes a real gap (unit tests never actually invoke the
   built binary).

## Parameter Choices

I am envisioning a small number to runs.  Try to test as many of
parameters combinations as possible.


Some thoughts


--algorithm:
    hc and ws     SAT only with large number of starts to
                  virtually guarantee success
    dfs and cdcl  SAT and UNSAT problems

--num-threads:  <none>, 2, 3, 4

--no-preprocessing or not

--alg-params
  dfs    val1 = 0/1
  cdcl   val1 = 0/1/2/3    val2 = 0/1/2/3/4*/5    * = num-threads > 1


## Testing

We might need a simple utility to independently check if a reported
solution is actually a solution.
  