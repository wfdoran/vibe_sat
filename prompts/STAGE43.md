# Stage 43

For this stage, let's do the next step from REPORT41.md.

2. **Try phase-selection techniques `vibe_sat` doesn't have yet**
   (tweak) — specifically *target phases* (Chanseok Oh; standard in
   CaDiCaL/Kissat since ~2015), and/or `REPORT33.md`'s already-scoped
   *periodic rephasing from a WalkSAT burst*. Cheap-to-moderate, real
   literature backing, untried here, plausible win on both random and
   some industrial instances.

The phase selection methods
- plain phase saving (current)
- target phases
- periodic rephasing / WalkSAT 
- one don't know about

Should we allow the user to select the method, or should we try
all them and pick the best one?

WalkSAT might solve the problem.  Rare, but could happen. 


## Questions

- Want a larger, more definitive confirmatory sweep on
  `polynomialBaseConflicts` specifically (30-50 files/directory) at
  some point, given this session's tooling flakiness prevented one, or
  is "no confident signal at the sample sizes tried" a good enough
  answer to move on from parameter tuning for now?

This seem sufficient.

- Given this stage's result reinforces `REPORT41.md`'s ranking, should
  Stage 43 move to the phase-selection tweaks (`REPORT41.md` #2:
  target phases, or the already-scoped periodic-rephasing-from-
  WalkSAT idea from `REPORT33.md`), or straight to scoping the
  watch-list representation work (#3), given you'd mentioned wanting
  to "mix that in at some point" rather than necessarily doing it
  next?

Going with phase selection.

