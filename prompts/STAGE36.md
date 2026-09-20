# Stage 36

Let's do the Learned clause minimization next.

3. **Learned clause minimization, new idea.** Also not on anyone's list.
   Recursive/self-subsumption minimization of a freshly learned clause
   (removing literals already implied by the clause's other literals via
   the implication graph — MiniSat has done this since 2005) typically
   shrinks learned clauses by a real, measurable margin with a cheap,
   well-specified algorithm. It directly helps item... see below.

Two possible issues I can see here.

- The run time of the clause minimization can be significant.  We had
  similar issues with preprocessing.  Ideally, it should run in
  O(clause log(clause)) time.  That may not be possible but strive for
  it.  Also, it would be good to some absolute time limit just in
  case.

- How does this work with CDCL?  Does the clause minimization routine
  "stop the world" not allowing any threads to make backtracking
  progress?  Or are you going to have fancy multi-threaded version
  which allows backtracking threads to continue to access the clause
  database while the minimization is in progress?

## Questions from REPORT35.md

- The bootstrap-not-interruptible limitation (Part 1, "A known,
  disclosed limitation") is a real, new finding, not something
  `STAGE35.md` asked to fix. Worth its own future stage (making
  `UnitPropagate`/the watch-selection loop/`occurrence.Build`
  interruptible), or is "the overall budget is now honestly measured
  and the search loop itself responds correctly" enough for now, given
  how invasive a real fix would be?

This is certainly good enough for now.
  
- `K=0.5` and `K=0.6` were statistically tied in the tuning sweep;
  `0.6` was picked without a strong reason to prefer it over `0.5`.
  Worth a larger/repeated sample to break the tie, or is either choice
  fine to leave as-is?

Either is fine.  When we get to listing internal parameters, make
sure this one.  
  
- `glucoseK` values above 0.8 (more frequent restarts) were never
  tested, since the trend pointed the other way. Confirming that
  omission doesn't bother you, or want it filled in for completeness?

Not a problem.

