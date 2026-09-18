# Stage 33

I think it is a good time for another brainstorming session.  No code
this time.  Below is a series of items from previous reports which we
have not acted on.  Then I added few new items that I thought up.  You
should scan through the old REPORTx.md and see if I missed anything
important.  Also, if you have any new ideas that have not been
previously mentioned, add them in.

Then research each and rank them on "bang for the buck".  How much
would it improve vibe_sat versus how much work it would be to
implement.

This will probably guide our next several stages.  


## Internal Parameter Optimization

REPORT22.md 

7. **List every internal parameter in one place.** Your item, first
   half. I count at least 15 scattered, code-comment-only tunables
   just in `cdcl` alone (Luby/polynomial/geometric restart bases and
   growth factor, LRB's alpha, clause/variable activity decay rates
   and rescale threshold, the clause-sharing export buffer's capacity
   and import cadence, the time-check interval) plus more in
   `dfs`/`hillclimb`/`preprocess`. Worth a single reference doc even
   before deciding whether to make any of them user-tunable.

## Fundamentally Different Algorithm

REPORT22.md

13. **Add a fundamentally different algorithm.** Your item, open-
    ended. I don't have a strong pitch of my own to name here without
    knowing what interests you -- a lookahead solver, a local-search/
    CDCL hybrid, and a MaxSAT variant are all real, different
    directions with different amounts of work. Tell me if one appeals
    and I'll turn it into a real STAGE proposal.


For now, I am not interested in MaxSAT.

A lookahead solver might interesting.  Is the cost of the lookahead
made up by a smaller search tree?

A local-search/CDCL hybrid is interesting.  I assume you use the
local search to drive the complete CDCL search.  Maybe you get
better variable selection heuristics.  You could also look at
belief propagation and/or survey propagation.

Scan the literature and find something interesting that you think
you can implement.

## Adaptive Novelty

REPORT22.md

14. **Adaptive Novelty+.** Your item; you already said low priority,
    and I agree -- `hc`/`ws` aren't the project's current focus.


Eventually, we should do this.

## Chronological Backtracking

REPORT22.md

15. **Chronological backtracking (Nadel & Ryvchin, SAT 2018).**
    Flagged in `REPORT7.md`'s original roadmap, never revisited. A
    real CDCL refinement, but a bigger lift than most items here;
    worth reconsidering after profiling shows backtracking cost
    actually matters.

I don't what the difference between this and non-chronological
backtracking is.

## LRB

REPORT22.md

15. **Chronological backtracking (Nadel & Ryvchin, SAT 2018).**
    Flagged in `REPORT7.md`'s original roadmap, never revisited. A
    real CDCL refinement, but a bigger lift than most items here;
    worth reconsidering after profiling shows backtracking cost
    actually matters.

## Luby Optimization

REPORT22.md

17. **Luby restart base recalibration.** `REPORT15.md` flagged that
    the literature-standard `b=100` visibly underperforms on this
    project's own benchmark set but kept it anyway, per STAGE15.md's
    own preference for a literature value when one exists. Small; I'd
    fold this into item 12's tuning harness rather than doing it ad
    hoc.


## Blocking-Literal Check

REPORT23.md

1. **Conditional blocking-literal check, gated by clause length.**
   The rejected optimization above is a real, standard technique that
   plausibly helps on longer clauses (industrial/structured instances
   with wide clauses, or long *learned* clauses even within a random-
   3-SAT run) even though it hurts on this project's mostly-length-3
   original clauses. Implementing it as `if clause.len() > N &&
   is_true(other_watch) { skip }` (some threshold, empirically tuned)
   would need real benchmarking on the `blocksworld`/`flat`/`ssa`
   structured sets added in Stage 21 to justify a specific threshold
   -- a real, if modest, follow-up project, not a quick fix.

## Different Watch-List 


REPORT23.md


3. **A genuinely different watch-list representation.** All of
   `propagate`'s cost is fundamentally the two-watched-literals
   scheme's own cost -- there's no single wasteful thing left to trim
   at the margins (this stage's one applied fix was the last "free"
   simplification I could find). Going further would mean a
   different algorithmic approach entirely (e.g. a lazier watch
   update scheme, or SIMD-friendly clause layout for the scan itself)
   -- real research-grade solver engineering, not a Stage 23 change.


## New: Smoke Test in CI

Add a small set of vibe_sat executions on the benchmark problems
to the CI script.  These would give end-to-end conformation that
vibe_sat is doing its job.  The problems can be easy; I just want
to test various parameters and make sure that everything is working
ok.


## New: SelectVar for DFS

In stead of scoring variables based on how many open clauses
clauses contain that variable, score its two literals.  Then
combine the scores of the literals to get the score of the
variable.

 score(x_i) = F(score(x_i positive), score(x_i negative))

what is F?  I don't know

  F(a,b) = min(a,b)
  F(a,b) = max(a,b)
  F(a,b) = 2 * min(a,b) + max(a,b)
  F(a,b) = (a + 1) * (b + 1)

What is not is

  F(a,b) = a + b

that is what we currently doing.

There must be literature on this?!

## New: Import recent papers

I could download all the papers from some recent SAT conferences
and have you run through them for any good ideas.

## New: Benchmarks

Are there any other benchmarks I should bring in?

