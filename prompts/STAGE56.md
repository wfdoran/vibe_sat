# Stage 56

Let's fix several small items from REPORT54.md this time.

2. **Range-validate the other internal parameters.** Only
   `glucoseWindowSize` crashes, so only that is validated now. But e.g.
   `varActivityDecay: 2.0` (and `0`) does not crash, yet uf250-01, which
   normally takes about 0.2 s, no longer reaches a verdict within 3 s.
   Rejecting decays outside (0, 1] and non-positive base conflict counts
   would turn silent degradation into a clear error. Zero/negative
   `lubyBaseConflicts`, `geometricGrowthFactor` etc. were tried and all
   still solved the instance, so they are lower priority.
   

Yes.  vibe_sat should return an error with unnatural parameters.
Be sure to document what the range is for each paramaeter in the
CLI help.

4. **Geometric restart threshold conversion** (`int(float64(base) *
   math.Pow(factor, n))`) is undefined in Go once the product exceeds int64.
   Unreachable in practice (roughly 94 geometric restarts already means about
   10^18 conflicts), so no action; noted for completeness. Rust saturates.

Can you saturate at INT64_MAX in go as well.  A quick check of the
float64 value before converting to an integer.  If you want to be sure
care, you do the computation in log-space and if log2(v) >= 63, set
to INT64_MAX. 

5. **Docs.** `docs/internal-parameters.md` line 39 should say
   `glucoseWindowSize` must be at least 1 (`docs/` is outside what this
   stage may edit).

Make this change.


## questions from REPORT55.md

1. **N=8 versus N=1?** Statistically a tie. If you prefer the simplest
   possible rule, N=1 plus the resync is the minimal fix. I left 8.

Leave it for 8 for now.  At some point we will do some testing on
a SMP machine with a large number of cores than this laptop.


2. **Rust slows down with N at 32 threads.** Worth a look if you plan to run
   32-thread Rust? It could be cache traffic from reading more peers'
   buffers, but I did not measure that.

Again, likely a limitation of this laptop.


3. **Different benchmark families.** All the evidence is on random 3-SAT
   UNSAT. A structured UNSAT family (not preprocessing-bound) would tell us
   whether importing more helps there.


Ok.  I will look for such problems.  