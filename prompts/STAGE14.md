# Stage 14

## phase

Let's implement the phase saving.

6. **Phase saving.** When a variable becomes unassigned (backtracking
   or a restart), remember the value it last held and default to that
   instead of a fixed/arbitrary polarity next time it's decided.
   Cheap, and a consistently measured win in MiniSat-lineage solvers.
   Related reference: Pipatsrisawat & Darwiche on component caching
   and related techniques in modern solvers (2007-era MiniSat/RSat
   literature).

## Command Line Parameter

I don't see any reason not to have phase saving be the default.
So, no change to any of the command line parameters.

