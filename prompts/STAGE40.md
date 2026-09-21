# stage 40

I recently installed minisat and cryptominisat5 on this laptop.  They
are both in /usr/bin.  For this stage, I want to do a time comparsion
to them versus our two vibe_sat programs (rust and go). 

Pick 20-30 problems with a variety of random vs industrial, sat vs unsat to
use in the comparsion.

For vibe_sat use CDCL.

For minisat and cryptominisat5, use their default settings.  

All programs should use 1 thread.

Report the runtimes on each problem with each solver.


