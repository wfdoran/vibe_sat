# Stage 64

From REPORT63.md, I would like start working on this.  

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


As a start, let's just add VMTF in this stage as one the possible
variable selection heuristics used with CDCL.  For the threaded
version add it into the mix.

--alg-params val1 == 4 will be VMTF

Do a quick survey of 5 CDCL variable selection algorithms.

