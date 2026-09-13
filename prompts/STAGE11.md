# Stage 11

## CDCL

This is going to a big one.  Let's implement CDCL + non-chron backtracking.  

3. **CDCL: conflict-driven clause learning + non-chronological
   backtracking.** This is the single biggest idea in the history of
   SAT solving and the natural next evolution of `dfs`'s existing BCP
   machinery: when propagation hits a contradiction, walk the
   implication graph to derive a new clause that explains *why*, add
   it to the formula, and jump back directly to the decision level
   where it becomes useful — instead of the current scheme's "try the
   other branch, one level up." This is what turns a plain DPLL search
   (what Stage 5 built) into a modern SAT solver. References: Marques-Silva
   & Sakallah, ["GRASP: A Search Algorithm for Propositional
   Satisfiability,"](https://ieeexplore.ieee.org/document/602316) 1996
   (later IEEE Trans. Computers 1999); Moskewicz et al., Chaff, above.


## Command Line Parameters

We will leave dfs as it is and call this a new algorithm: cdcl.

  --algorithm cdcl  -a cdcl

Initially, the only --alg-params will be the choice of SelectVar,
same as dfs.

## Benchmark

When you get done, rerun the uf250-1065 and uuf250-1065.  This
should give a good comparision between cdcl and dfs.  

## Questions

Please answer the following questions in your report.
- Are the added conflict clauses used as part of the SelectVar?
- Is there a limit on how many conflict clauses can be retained?
  Do we need the user to pass some memory limit?

