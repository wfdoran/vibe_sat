# Stage 61

From REPORT50.md, lets try

6. **Bounded Variable Addition (new idea, above).** Real literature-
   backed technique, natural companion to existing BVE machinery,
   plausible win on the same instance classes BVE already helps.


## Benchmarks

From REPORT55.md,

3. **Different benchmark families.** All the evidence is on random 3-SAT
   UNSAT. A structured UNSAT family (not preprocessing-bound) would tell us
   whether importing more helps there.

I added benchmark/bf, benchmark/pigeon-hole, and benchmark/pret.  These are
all structured problem.  I have no idea how easy or hard they are.


## Questions from REPORT60.md

- **Memory limit.** Should `cdcl` get a default clause-memory limit,
  or something adaptive? It looks like the largest single speed lever
  found in a while, but I only tested small random instances.

At some point we should have a memory limit of 80%+- of the
available memory, but allow the user to set it with a command
line parameter.  For now let's now worry about it, but include this
in the next brainstorming stage.

- **Unit-propagation worklist.** Want it as its own stage (section 3)?
  It would also fix the Stage 57 unit-chain regression.

I added this to my todo list.

- **Preprocessing budgets.** Want a different measurement approach for
  them (section 4.5)? Or leave the defaults as they are.

They are fine. 

- **Geometric restarts.** The sweep suggests the optimum is flat and
  noisy; it is only used by the multi-thread round-robin, so I left
  it.

agreed.

- **Sample size.** Each sweep used 24 files. Noise is about 7%,
  so effects under about 10% should be treated as ties. The two
  changes made are large (15%+, and 9 to 10 files unsolved vs 0).

It is slow enough as is.  When sweeping all of the params, leave it as
is.  If in the future, we want to dig in one parameter, expand the
number of sample files.


- **Untracked benchmark changes.** `git status` shows
  `benchmark/notes.txt` modified and `benchmark/bf`,
  `benchmark/pigeon-hole`, `benchmark/pret` untracked.  These are not
  mine.

They are commited now

- **Timing hygiene.** Sweeps ran sequentially with the
  machine otherwise idle except for light file edits; I did not pin
  CPUs.

That is the right thing to do.