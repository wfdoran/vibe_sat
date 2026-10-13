# Stage 66

Now that we have VMTF, I want to return to mode switching idea from
REPORT63.md.

**1. Stable/focused mode switching (Kissat's core architecture).** Kissat
alternates between two search modes inside one run: a *focused* mode (VMTF
variable ordering, fast Glucose-style restarts) and a *stable* mode (VSIDS,
slow restarts, target phases), switching on a growing schedule. Neither is a
new idea (Chanseok Oh, SAT 2015, is the cited origin), but this project has none
of it, and every winner above uses it. It connects directly to what we measured:
Stage 59 found `target` phase is best on SAT and worst on UNSAT, and today we
get that mix by running *different threads* with different strategies. Mode
switching gets both in one thread. It would also need **VMTF**, which we do not
have (a cheap structure: a linked list moved to the front on bump). Effort:
moderate. Payoff: plausibly the largest single item here for the 1-thread
default.


My suggestion:

- Add new variable selection heuristic (val1 == 5) which for 1-thread
  toggles between VMTF and VSIDS on each restart.

- When using this switching heuristic with --num_threads == 1, the
  value of the second --alg-params would be disregarded.  Instead, we
  would interlace luby with quadratic for the restart strataegy.  The
  luby sequence would use with VMTF starts and quadratic with the
  VSIDS starts.

- With --num_threads > 1 and --alg-params val1 == 5, randomly select
  half of the threads to use VMTF/Luby and the other half use
  VSIDS/Geometric.  Make 50% a parameter which we can sweep over
  later.  But always have at last 1 or each type. 


Questions:
- Should we use geometric over quadratic with VSIDS?  If yes, don't
  start on this work and tell me why?

- Do you have a better way to implement the switching mode?  If yes,
  don't start on this work and tell me your idea?



  