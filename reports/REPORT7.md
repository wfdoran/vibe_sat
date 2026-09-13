# Report: Stage 7

## What this stage is

Per `STAGE7.md`, this stage involves no code changes to `go_src` or
`rust_src`. Instead, it's a literature-grounded brainstorm: an ordered
list of roughly ten candidate next steps for `vibe_sat`, informed by
the history of SAT solving research and by where the project already
stands after Stages 1–6 (DIMACS parsing; a simple hill-climb; WalkSAT;
`--help`; a complete DPLL-style depth-first search with unit
propagation (BCP); and a choice of two branching heuristics).

## How I approached this

I drew on established SAT solving literature I already know well
(the DPLL → CDCL lineage: GRASP, Chaff, MiniSat), and then ran a few
web searches to check my knowledge against current practice and catch
anything more recent than my training — specifically on branching
heuristic comparisons (VSIDS vs. LRB vs. CHB) and on what the current
SAT Competition-winning solvers (Kissat, CaDiCaL) are actually doing
in 2024–2025. Sources are linked inline below.

## The ordered list

I ordered this as a sensible *build sequence* — each item either is a
direct prerequisite for the next, or is independent and placed by how
much value it would add relative to how much it would cost to build,
given what already exists in `internal/dfs`/`src/dfs.rs` and
`internal/hillclimb`/`src/hillclimb/`.

1. **Preprocessing** (root-level unit propagation, pure literal
   elimination, subsumption, bounded variable elimination). This is
   explicitly pre-announced in `STAGE5.md` itself ("At a later Stage,
   we will add a Preprocessing step before starting any solving
   algorithm") and is the cheapest, lowest-risk item on this list: it
   benefits `hc`, `ws`, *and* `dfs` simultaneously, for free, by
   shrinking/simplifying the formula once before any algorithm sees
   it. Classic reference: Eén & Biere, ["Effective Preprocessing in
   SAT Through Variable and Clause
   Elimination"](https://www.cs.cmu.edu/~emc/15-820A/reading/een-biere-2005.pdf)
   (SatELite, 2005).

2. **Watched literals for BCP.** The current `BCP`/`bcp` rescans the
   occurrence lists built in Stage 2; the standard, much faster
   technique is to "watch" only two not-yet-falsified literals per
   clause and only re-examine a clause when one of its watched
   literals changes. This is an internal data-structure upgrade, not
   a new algorithm, but it's the engineering foundation that makes
   everything below it (especially CDCL, which calls BCP *far* more
   often, once per conflict) practical at scale. Reference: Moskewicz,
   Madigan, Zhao, Zhang & Malik, ["Chaff: Engineering an Efficient SAT
   Solver"](https://www.cs.princeton.edu/~zkincaid/courses/fall22/readings/SATChaff.pdf),
   DAC 2001.

3. **CDCL: conflict-driven clause learning + non-chronological
   backtracking.** This is the single biggest idea in the history of
   SAT solving and the natural next evolution of `dfs`'s existing BCP
   machinery: when propagation hits a contradiction, walk the
   implication graph to derive a new clause that explains *why*, add
   it to the formula, and jump back directly to the decision level
   where it becomes useful — instead of the current scheme's "try the
   other branch, one level up." This is what turns a plain DPLL search
   (what Stage 5 built) into a modern SAT solver. References: Marques-Silva
   & Sakallah, ["GRASP: A Search Algorithm for Propositional
   Satisfiability,"](https://ieeexplore.ieee.org/document/602316) 1996
   (later IEEE Trans. Computers 1999); Moskewicz et al., Chaff, above.

4. **Modern branching heuristics (VSIDS, and/or LRB/CHB).** Once
   conflicts exist to learn from, `SelectVar`/`select_var` can be
   replaced with an activity-based heuristic that scores variables by
   how often they appear in *recent conflict clauses*, rather than the
   static/structural scoring from Stages 5–6. I checked this against
   current literature rather than relying only on older knowledge:
   Liang, Ganesh, Poupart & Czarnecki's Learning Rate Branching (LRB)
   heuristic reportedly beats both VSIDS and the Conflict History-Based
   (CHB) heuristic on SAT Competition 2009–2014 instances (1279 vs.
   1179 vs. 1235 solved), and combining VSIDS/CHB (e.g. MapleCOMSPS's
   switch-or-alternate strategy) is itself an active research
   direction. References: Liang et al., ["Learning Rate Based
   Branching Heuristic for SAT
   Solvers"](https://cs.uwaterloo.ca/~ppoupart/publications/sat/learning-rate-branching-heuristic-SAT.pdf),
   SAT 2016; Cherif, ["Combining VSIDS and CHB Using
   Restarts"](https://drops.dagstuhl.de/storage/00lipics/lipics-vol210-cp2021/LIPIcs.CP.2021.20/LIPIcs.CP.2021.20.pdf),
   CP 2021.

5. **Restarts** (Luby sequence, or simple geometric growth). Cheap to
   add once CDCL exists: periodically abandon the current decision
   stack and start over from the root, keeping every learned clause
   collected so far. This escapes runs of bad early decisions and is
   one of the best effort-to-payoff ratios in this whole list.
   References: Luby, Sinclair & Zuckerman, "Optimal Speedup of Las
   Vegas Algorithms," 1993; Gomes, Selman & Kautz, "Boosting
   Combinatorial Search Through Randomization," AAAI 1998.

6. **Phase saving.** When a variable becomes unassigned (backtracking
   or a restart), remember the value it last held and default to that
   instead of a fixed/arbitrary polarity next time it's decided.
   Cheap, and a consistently measured win in MiniSat-lineage solvers.
   Related reference: Pipatsrisawat & Darwiche on component caching
   and related techniques in modern solvers (2007-era MiniSat/RSat
   literature).

7. **Learned-clause database management.** Once CDCL is running for a
   while, thousands of learned clauses accumulate; periodically
   deleting the least "active" ones (MiniSat-style) bounds memory and
   keeps propagation fast. A necessary companion to #3, not useful on
   its own before it.

8. **Chronological backtracking.** A genuinely recent refinement (not
   something from the 1990s): backtrack only to the *previous*
   decision level after a conflict instead of computing the
   second-highest level in the conflict clause, which is simpler and
   was shown to help in practice despite initially seeming like a step
   backward. I flagged this one specifically because my web search
   surfaced it as an active, fairly recent research thread, not just
   textbook material. Reference: Nadel & Ryvchin, ["Chronological
   Backtracking,"](https://www.researchgate.net/publication/325968980_Chronological_Backtracking)
   SAT 2018; see also Möhle & Biere, ["Backing
   Backtracking,"](https://fmv.jku.at/papers/MoehleBiere-SAT19.pdf)
   SAT 2019, for follow-up analysis.

9. **Better local search (Novelty / Novelty+ / Adaptive Novelty+).** An
   independent, much cheaper-to-build alternative to the CDCL arc
   above: a direct upgrade path for the existing `hc`/`ws` code from
   Stages 2 and 4, adding recency-based tie-breaking and/or
   self-tuning the noise parameter during the run instead of using a
   fixed value, typically outperforming plain WalkSAT/SKC with only a
   modest implementation cost. Reference: McAllester, Selman & Kautz,
   "Evidence for Invariants in Local Search," AAAI 1997; Hoos, "An
   Adaptive Noise Mechanism for WalkSAT," AAAI 2002.

10. **Multi-core / portfolio parallelism.** `PROMPT.md` explicitly
    anticipated this from the very start ("Eventually, we are going to
    run with multiple cores... Be prepared for this"), and now there
    are enough distinct algorithms/configurations (`hc`, `ws`, `dfs`,
    eventually CDCL) to make a *portfolio* approach worthwhile: run
    several concurrently (goroutines in Go, threads/tokio in Rust) and
    return whichever finishes first. This is the simplest genuinely
    parallel SAT strategy and a natural fit for the concurrency both
    language runtimes are good at; full clause-sharing parallel CDCL
    (à la Plingeling/ManySAT) would be a much bigger, longer-term
    version of the same idea.

11. **A proper Go-vs-Rust benchmarking harness.** This directly serves
    the project's own stated top-level motivation —
    "[to] understand strengths and weaknesses of each language"
    (`PROMPT.md`). Stages 4 and 6 each used a one-off, throwaway
    comparison script (deleted after use, per the project's own
    cleanup convention) to get real numbers for those reports. Turning
    that into a standing, repeatable tool — running both binaries
    across all of `benchmark/`, across all algorithms, and reporting
    timing/node-count tables — would make the project's central
    comparison a first-class, reusable artifact instead of an ad hoc
    one-off each time.

## A couple of further-out honorable mentions

Not part of the core ordered ten, but worth having on record for later:

- **DRAT proof logging** for UNSAT certificates (Wetzler, Heule &
  Hunt, "DRAT-trim: Efficient Checking and Trimming Using Expressive
  Clausal Proofs," SAT 2014) — since `dfs` is already a *complete*
  solver capable of proving UNSAT, emitting a machine-checkable proof
  (verifiable by an independent tool) would be a nice capstone on that
  capability, though it's a substantial lift on its own.
- **Inprocessing** (interleaving preprocessing techniques *during* the
  search, not just once up front) — current state-of-the-art solvers
  like Kissat and CaDiCaL lean heavily on this, per their SAT
  Competition 2024/2025 solver description papers. It's really a
  more advanced version of item #1 above, but meaningfully harder to
  implement correctly, which is why I've separated it out rather than
  folding it into the preprocessing item.

## Sources consulted

- [Learning Rate Based Branching Heuristic for SAT Solvers](https://cs.uwaterloo.ca/~ppoupart/publications/sat/learning-rate-branching-heuristic-SAT.pdf)
- [Combining VSIDS and CHB Using Restarts in SAT](https://drops.dagstuhl.de/storage/00lipics/lipics-vol210-cp2021/LIPIcs.CP.2021.20/LIPIcs.CP.2021.20.pdf)
- [Understanding VSIDS Branching Heuristics in Conflict-Driven Clause-Learning SAT Solvers](https://arxiv.org/pdf/1506.08905)
- [SAT Competition 2024 Medals](https://cca.informatik.uni-freiburg.de/sat24medals/)
- [CaDiCaL, Gimsatul, IsaSAT and Kissat Entering the SAT Competition 2025](https://cca.informatik.uni-freiburg.de/papers/BiereFallerFleuryFroleyksPollitt-SAT-Competition-2025-solvers.pdf)
- [CaDiCaL, Gimsatul, IsaSAT and Kissat Entering the SAT Competition 2024](https://cca.informatik.uni-freiburg.de/papers/BiereFallerFazekasFleuryFroleyksPollitt-SAT-Competition-2024-solvers.pdf)
- [Chronological Backtracking (Nadel & Ryvchin, SAT 2018)](https://www.researchgate.net/publication/325968980_Chronological_Backtracking)
- [Backing Backtracking? (Möhle & Biere, SAT 2019)](https://fmv.jku.at/papers/MoehleBiere-SAT19.pdf)

## Open questions / notes for you

- This report contains no code changes, matching `STAGE7.md`'s
  instructions.
- I ranked by "sensible build sequence," not strictly by expected
  SAT-competition-style performance impact — let me know if you'd
  rather see it re-ordered by, say, implementation effort, or split
  into a Go-first/Rust-first track, or if one of the "further-out"
  items should be promoted into the main ten.
- Items 1–8 form one coherent arc (turning the existing DPLL-style
  `dfs` into a genuine CDCL solver); item 9 is a much smaller,
  independent improvement to the existing local-search code; items 10
  and 11 are about the project's infrastructure/goals rather than the
  solver's algorithms per se. Happy to pick any subset as the actual
  next stage whenever you're ready.
