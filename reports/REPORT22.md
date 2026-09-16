# Report: Stage 22

## What this stage is

Per `STAGE22.md`, no code changes this stage: a planning discussion.
I sorted your list, everything I could find in `reports/REPORT1.md`
through `REPORT21.md` that was raised and never done, and a few ideas
of my own, into one ordered list. My actual recommendation for what
to do next is short: **profile first (item 1 below), then let the
results decide between the two biggest-payoff candidates (parallel
preprocessing and the `dfs` deque rewrite) rather than guessing now.**
Everything past that is genuinely open to your preference.

## How I searched for "Old Items"

I read all 21 prior reports' "Open questions"/"notes for you"
sections plus `REPORT7.md`'s original 11-item roadmap (the project's
first "what's next" discussion, back when `hc`/`ws` were the only
algorithms), and cross-checked every candidate forward through later
reports to confirm it was genuinely never addressed, not just phrased
differently later. A few items turned out to be *partially* done,
which I've noted individually rather than dropping.

## The full list, ordered

### Tier 1 -- do this first

1. **Profile the CDCL algorithm.** Your item, and I'd put it above
   everything else for one concrete reason: Stage 21 already showed
   that guessing where time goes is unreliable -- I was confident two
   benchmark instances would show a threading speedup and instead
   found, only after adding timing instrumentation, that
   single-threaded preprocessing was consuming 99.7% of the wall
   clock. `REPORT10.md` flagged the Go-vs-Rust performance-gap
   *cause* as never investigated, and `REPORT19.md` deferred
   work-stealing idle/steal-time profiling to "a dedicated profiling
   stage" that never happened. Both items 3 and 4 below are
   downstream of this one -- I'd rather have real numbers before
   picking between them than guess now. Cheap, safe, no design risk.

### Tier 2 -- the two candidates profiling should decide between

2. **Parallelize preprocessing.** Your item, directly motivated by
   the Stage 21 finding above (`bw_large.c`/`.d`, `ssa7552-*`: ~11s
   single-threaded preprocessing vs. ~30ms of already-parallel `cdcl`
   search). Real, measured evidence this matters on real instances --
   but profiling should say *which* preprocessing step (unit
   propagation, pure-literal, subsumption, or BVE) is actually the
   cost before parallelizing the wrong one. My guess is BVE (pairwise
   clause resolution scales badly), but it's a guess.
3. **Go's `dfs` deque: lock-free rewrite.** Not on your list, but it's
   the strongest *already-confirmed* performance finding sitting
   unaddressed anywhere in the project: two independent benchmarks
   (`REPORT18.md`'s `uf250` SAT numbers, `REPORT19.md`'s `uuf175`
   UNSAT numbers) show Go's hand-rolled mutex-guarded deque plateauing
   past 4 threads while Rust's lock-free `crossbeam-deque` keeps
   scaling to 16. I flagged it in `REPORT19.md`'s open questions and
   never returned to it. A real design change (an atomic-pointer-swap
   or Chase-Lev-proper lock-free deque in Go), not a quick fix.

### Tier 3 -- your other two performance items, lower urgency

4. **Clause-database contention under real stress.** Your item. The
   Stage 20 prototype validated the *design* is low-contention in
   isolation (tens of millions of reads, zero corruption, race-clean);
   it was never stress-tested under a real many-thread `cdcl` run
   doing real work simultaneously. Given the design is already
   lock-free, I don't expect a problem, but "I don't expect one" isn't
   the same as measuring one -- pairs naturally with item 1's
   profiling pass rather than needing its own investigation.
5. **Try Option D for CDCL.** Your item; you already created a `CDCL`
   branch in Stage 21 for exactly this. I'd wait for profiling/real
   workloads to surface a case where Option B's portfolio approach
   underperforms before spending the implementation effort -- Option
   B is only one stage old and already showing real 3-4x speedups
   (`REPORT21.md`).

### Tier 4 -- documentation and legalese, no dependencies, can happen anytime

6. **README sub-pages: usage, sample runs, background, references.**
   Your item. Real gap -- the current `README.md` is 81 lines, covers
   build/test only, no usage examples or algorithm background.
7. **List every internal parameter in one place.** Your item, first
   half. I count at least 15 scattered, code-comment-only tunables
   just in `cdcl` alone (Luby/polynomial/geometric restart bases and
   growth factor, LRB's alpha, clause/variable activity decay rates
   and rescale threshold, the clause-sharing export buffer's capacity
   and import cadence, the time-check interval) plus more in
   `dfs`/`hillclimb`/`preprocess`. Worth a single reference doc even
   before deciding whether to make any of them user-tunable.
8. **License + Warranty disclaimer.** Your item. There's currently no
   `LICENSE` file and no warranty language anywhere -- this needs
   *your* choice of license (MIT? Apache-2.0? something else?), not
   mine; I'll write the boilerplate once you tell me which.
9. **UNSAT proof logging (DRAT) + independent checker.** New idea, not
   previously raised anywhere. Every verification sweep across 21
   stages independently re-checks a *SAT* verdict (parse the returned
   assignment, confirm every clause is satisfied from scratch) but
   never independently checks an *UNSAT* verdict on anything outside
   the labeled `uf`/`uuf` SATLIB sets -- it's trusted on cross-language
   agreement alone. Emitting a DRAT (or simpler resolution) proof
   trace from `cdcl` and checking it with an off-the-shelf checker
   like `drat-trim` would close that gap for real. A genuinely new
   verification capability, not just more testing of what's already
   tested.
10. **CI (GitHub Actions).** New idea. Cheap hygiene: run both test
    suites, `gofmt`/`go vet`, `cargo fmt --check`/`clippy` on every
    push. Nothing in the project currently runs any of this
    automatically -- it all happens because I remember to.

### Tier 5 -- worth deciding, but I'd like your input first

11. **Progress meter.** Your item; you said you're not sure how to do
    this, and honestly, neither am I in the way you might be
    picturing it: a true "% complete" isn't really knowable for SAT
    search (you don't know the size of the remaining tree, or whether
    the next decision resolves everything or triggers ten thousand
    more conflicts). What *is* easy and would give similar practical
    reassurance on long runs: a periodic heartbeat line at a high
    `--verbose` level ("312,000 conflicts, 4,800 restarts, 45s
    elapsed, still searching"), the same idea `--verbose >= 2`'s "new
    best score" already uses for `hc`/`ws`, just time-gated instead of
    improvement-gated. Let me know if that's the kind of thing you had
    in mind, or if you meant something else.
12. **Auto-tuning harness + `.vibe_sat.json` config.** Your item,
    second and third parts. I'd sequence this as: document the
    parameters (item 7) -> build a harness that sweeps them against
    the benchmark set and reports what's actually better -> *then*
    decide which ones are worth exposing to a config file, since
    exposing a parameter nobody ever benefits from tuning is just
    surface area. A bigger investment than it looks; happy to scope
    it properly if you want to prioritize it.
13. **Add a fundamentally different algorithm.** Your item, open-
    ended. I don't have a strong pitch of my own to name here without
    knowing what interests you -- a lookahead solver, a local-search/
    CDCL hybrid, and a MaxSAT variant are all real, different
    directions with different amounts of work. Tell me if one appeals
    and I'll turn it into a real STAGE proposal.

### Tier 6 -- low priority / small, can wait indefinitely

14. **Adaptive Novelty+.** Your item; you already said low priority,
    and I agree -- `hc`/`ws` aren't the project's current focus.
15. **Chronological backtracking (Nadel & Ryvchin, SAT 2018).**
    Flagged in `REPORT7.md`'s original roadmap, never revisited. A
    real CDCL refinement, but a bigger lift than most items here;
    worth reconsidering after profiling shows backtracking cost
    actually matters.
16. **LRB's omitted refinements** (reason-side-rate bonus, annealed
    alpha -- `REPORT13.md`). `REPORT13.md`'s "LRB underperforms VSIDS
    on this benchmark set" verdict was explicitly provisional pending
    these; never revisited. Worth it only if you want a more
    definitive answer on LRB specifically.
17. **Luby restart base recalibration.** `REPORT15.md` flagged that
    the literature-standard `b=100` visibly underperforms on this
    project's own benchmark set but kept it anyway, per STAGE15.md's
    own preference for a literature value when one exists. Small; I'd
    fold this into item 12's tuning harness rather than doing it ad
    hoc.
18. **A standing Go-vs-Rust benchmark harness.** `REPORT7.md`'s
    original item 11 -- every stage since has used a one-off,
    thrown-away comparison script instead. Nice infrastructure, not
    urgent.
19. **Finish `REPORT16.md`'s per-thread `--verbose` breakdown.**
    Totals are already summed across workers (`NumDecisions`/
    `NumConflicts`/`NumNodes`); a per-thread breakdown at a higher
    verbosity level was floated but never built. Small, cosmetic.
20. **Diagnose `dfs`'s non-monotonic UNSAT speedup** past 8-16
    threads (`REPORT19.md`): tree-exhaustion vs. a real scaling limit
    was never disambiguated. Minor, low urgency.
21. **Expand the structured/industrial benchmark corpus** beyond
    `blocksworld`/`flat125`/`flat200`/`ssa`. Ties naturally to items 2
    and 4 above (clause sharing's benefit is much more visible on
    industrial instances per `REPORT20.md`'s own literature citation)
    -- worth doing alongside those, not on its own.
22. **Run a full benchmark sweep to exhaustion**, not a capped sample
    (`REPORT10.md`). Every sampled sweep so far has been clean; I
    don't see a concrete reason to believe exhaustive sweeping would
    turn up anything the sampling has missed, so I'd leave this as
    background confidence-building rather than a real priority.

## Summary of my recommendation

Start with **item 1 (profiling)**. It's cheap, safe, and every other
performance item on this list (2, 3, 4, 15) is currently a guess about
where time actually goes -- Stage 21 already taught me that guess can
be wrong. Once the numbers are in, I'd expect them to point clearly at
either preprocessing (item 2) or the `dfs` deque (item 3) as the
better next investment, rather than needing to build both blind.
Documentation and legalese (items 6-10) have no dependencies and could
run in parallel with that if you'd rather make visible progress on
both fronts at once. Everything in Tier 5 needs a decision from you
before I'd start it; everything in Tier 6 I'd leave alone until
something above it creates a reason to revisit.

## Questions for you

- Which license for the `LICENSE` file (item 8)? MIT and Apache-2.0
  are the common defaults for something like this; no strong opinion
  from me either way.
- Is the periodic-heartbeat idea (item 11) what you had in mind for a
  progress meter, or something else?
- Any pull toward a specific "fundamentally different algorithm"
  (item 13), or should I come back with a couple of concrete options
  once other work clears?
- Does starting with profiling (item 1) sound right, or would you
  rather I sequence something else first?
