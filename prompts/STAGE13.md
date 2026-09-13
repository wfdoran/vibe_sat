# Stage 13

## Variable Selection Heuristics

Let's add both VSIDS and LRB/CHB. 

4. **Modern branching heuristics (VSIDS, and/or LRB/CHB).** Once
   conflicts exist to learn from, `SelectVar`/`select_var` can be
   replaced with an activity-based heuristic that scores variables by
   how often they appear in *recent conflict clauses*, rather than the
   static/structural scoring from Stages 5–6. I checked this against
   current literature rather than relying only on older knowledge:
   Liang, Ganesh, Poupart & Czarnecki's Learning Rate Branching (LRB)
   heuristic reportedly beats both VSIDS and the Conflict History-Based
   (CHB) heuristic on SAT Competition 2009–2014 instances (1279 vs.
   1179 vs. 1235 solved), and combining VSIDS/CHB (e.g. MapleCOMSPS's
   switch-or-alternate strategy) is itself an active research
   direction. References: Liang et al., ["Learning Rate Based
   Branching Heuristic for SAT
   Solvers"](https://cs.uwaterloo.ca/~ppoupart/publications/sat/learning-rate-branching-heuristic-SAT.pdf),
   SAT 2016; Cherif, ["Combining VSIDS and CHB Using
   Restarts"](https://drops.dagstuhl.de/storage/00lipics/lipics-vol210-cp2021/LIPIcs.CP.2021.20/LIPIcs.CP.2021.20.pdf),
   CP 2021.

## Command Line Parameters

The first --alg-params value will now allow two additional values (2
and 3) to support these heuristics.  Also, you can decide which one
should be the default when using --algorithm=cdcl
