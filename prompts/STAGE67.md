# Stage 67

Let's look at this next. 

**2. A scheduled clause-database reduction, with Kissat-style tiers
(carry-over from `REPORT60.md`).** Section 3's dynamic tier limits are a refined
version of what we already have (LBD, a glue threshold of 2). The bigger point
is `REPORT60.md`'s finding: a 100 KB learned-clause limit made the same files
about **3.2x faster** than no limit. Kissat does not wait for a memory limit: it
reduces the clause database on a **conflict-count schedule** **(my knowledge)**
and keeps tier-1/tier-2 clauses. The survey therefore suggests the fix may be
"reduce periodically by default" rather than "pick a memory limit". Your
decision was to bring the memory-limit question to the *next brainstorming
stage*; consider this its input. A 2026 paper (*Rethinking Clause Management*,
abstract only) argues LBD alone fails on arithmetic-circuit instances and
proposes separating a clause's "inherent lineage" from its "dynamic usage",
reporting up to 5.74x on multiplier problems, so there may be a second
refinement later.



What the code does today

- Reduction only runs past a memory limit. reduceClauseDatabase is called only when -p _ _ <limit> is set and estimatedBytes exceeds it. By default it never runs, so the database grows without bound.
- What it deletes. When it runs, it deletes the worse half of the learned clauses that are neither locked as a reason nor glue (LBD ≤ 2). The order is LBD descending, then activity ascending.
- No tiers. Only the single glue threshold protects clauses.
- The rebuild is costly. The function rebuilds every index structure from scratch. That is O(whole database) per call, so how often it runs matters.

Proposed changes

1. Scheduled reduction.
   - Reduce on a conflict-count schedule, e.g. the first reduction after reduceFirst conflicts and the interval growing by reduceInc each time (Kissat uses a sqrt-like growth).
   - The memory limit stays as an additional trigger.
   - Add cdcl.reduceFirstConflicts and cdcl.reduceIncrement, both settable and sweepable. Setting the first to 0 turns it off, which gives a clean baseline.
2. Tiers.
   - Tier 1 (LBD ≤ 2, today's glue) is kept forever.
   - Tier 2 (LBD ≤ ~6) is kept while it was used recently. "Used" means it took part in conflict analysis since the last reduction.
   - Tier 3 is the rest. Each reduction deletes a fraction of it, ranked by LBD then activity.
   - New parameters: tier2LBD, reduceFraction (today it is fixed at 0.5), and a "recently used" window.
   - The "used since last reduction" flag per clause is new state, and Go and Rust must keep it identical.
3. Defaults. The scheduled reduction would be on by default only if it wins the measurements below. Otherwise it ships off and the report says so, as in Stage 66.
4. Parity and tests.
   - Go and Rust get the same change.
   - Tests: brute-force fuzz with very frequent reductions (the Stage 66 style), an UNSAT instance, and a check that locked clauses survive.
   - Docs: internal-parameters.md, background.md, -h if it lists any of this, and references.md.
5. Measurement.
   - Compare no reduction, the 100 KB limit, and scheduled reduction (with and without tiers).
   - Sets: the two random samples from REPORT60, plus structured sets, the largest instances and multi-thread. REPORT60 only tested small random 3-SAT, so a gain there could be an artifact.


Sounds good.


Decisions for you

1. Scope. Should I do the schedule and the tiers together, or the schedule first with tiers as a follow-up stage? I recommend both together, since the schedule is the main fix and tiers are what keep it from hurting structured instances. It is a bigger diff, though.

Do both together.

2. Default. Do you want me to switch the default to scheduled reduction if it wins, or only add it?

Measure it.  Then have some parameter --database-toggle (what single letters are free?) to switch to the other.

3. Multi-thread. Should all threads use the same schedule, or should it vary per thread as restarts and phases do? I recommend the same schedule, with the parameters exposed for your sweeps.

Same schedule for each thread.

4. Memory-limit semantics. Should an explicit val3 limit keep its current meaning as an extra trigger? I recommend yes, since that keeps old command lines working.

Keep it.  It is an additional trigger.


The 2026 paper

I'd skip it for now. The abstract only claims big gains on multiplier circuits, and we have none in the benchmark set. We also need the basic schedule first, so we have a baseline to compare it against. If you do fetch it, its useful part would be the "inherent lineage vs dynamic usage" scoring, which would only matter as a later refinement to how tier 3 is ranked. A follow-up stage could take it, once we have a circuit-type benchmark.
