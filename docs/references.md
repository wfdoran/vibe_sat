# References

Papers that had a direct, traceable influence on some part of
`vibe_sat`'s design — either an algorithm implemented as described, or
a design decision made after reading it. Grouped roughly by the part
of the solver each one shaped; see [background.md](background.md) for
how the ideas fit together, and the cited `reports/REPORTx.md` files
for the full story (including, in several cases, this project's own
benchmark measurements disagreeing with a paper's headline claim —
flagged explicitly where that happened).

## Local search (`hc`, `ws`)

- Selman, Kautz & Cohen, ["Noise Strategies for Improving Local
  Search,"](https://www.cs.cornell.edu/selman/papers/pdf/94.aaai.walksat.pdf)
  AAAI 1994. The original WalkSAT (WalkSAT/SKC) algorithm — the noise
  parameter and greedy/random flip choice `ws` implements directly.
  (`reports/REPORT4.md`)
- McAllester, Selman & Kautz, "Evidence for Invariants in Local
  Search," AAAI 1997, and Hoos, "An Adaptive Noise Mechanism for
  WalkSAT," AAAI 2002. Surveyed as natural next steps for local search
  (Novelty/Novelty+/Adaptive Novelty+) but not implemented.
  (`reports/REPORT7.md`, `reports/REPORT22.md`)

## Complete search (`dfs`, `cdcl`) core algorithm

- Marques-Silva & Sakallah, ["GRASP: A Search Algorithm for
  Propositional Satisfiability,"](https://ieeexplore.ieee.org/document/602316)
  IEEE Trans. Computers, 1999 (conference version 1996). Conflict-driven
  clause learning and non-chronological backtracking — the core idea
  behind `cdcl`. (`reports/REPORT11.md`)
- Moskewicz, Madigan, Zhao, Zhang & Malik, ["Chaff: Engineering an
  Efficient SAT
  Solver,"](https://www.cs.princeton.edu/~zkincaid/courses/fall22/readings/SATChaff.pdf)
  DAC 2001. Watched literals (`dfs`'s and `cdcl`'s BCP) and the VSIDS
  branching heuristic. (`reports/REPORT9.md`, `reports/REPORT13.md`)
- Eén & Sörensson, "An Extensible SAT-solver," SAT 2003 (MiniSat).
  Clause activity bumping/decay and the learned-clause database
  reduction policy `cdcl` uses. (`reports/REPORT12.md`)
- Audemard & Simon, "Predicting Learnt Clauses Quality in Modern SAT
  Solvers," IJCAI 2009 (Glucose). "Literal Block Distance" (LBD)
  scoring for learned clauses — `cdcl`'s glue-clause protection and
  LBD-primary sort in its clause-deletion policy (`--alg-params
  val2=4`'s restart policy is also this paper's; see "Restarts"
  below). (`reports/REPORT33.md`, `reports/REPORT34.md`)
- Sörensson & Biere, "Minimizing Learned Clauses," SAT 2009 (formalizing
  a heuristic MiniSat itself has used since 2005). Recursive
  self-subsumption minimization of a freshly learned clause, removing
  literals already implied by the clause's other literals via the
  implication graph. (`reports/REPORT36.md`)
- Nadel & Ryvchin, "Chronological Backtracking," SAT 2018, and Möhle &
  Biere, "Backing Backtracking?," SAT 2019. Surveyed as a possible
  refinement to `cdcl`'s backjumping; not implemented.
  (`reports/REPORT7.md`)

## Branching heuristics

- Liang, Ganesh, Poupart & Czarnecki, ["Learning Rate Based Branching
  Heuristic for SAT
  Solvers,"](https://cs.uwaterloo.ca/~ppoupart/publications/sat/learning-rate-branching-heuristic-SAT.pdf)
  SAT 2016. The LRB heuristic `cdcl` implements as `--alg-params`
  `val1=3`. **This project's own benchmark measurement found VSIDS
  clearly ahead of LRB** on its uniform random 3-SAT set, the opposite
  of this paper's headline SAT-Competition result — VSIDS, not LRB, is
  `cdcl`'s default. (`reports/REPORT13.md`)
- Cherif, "Combining VSIDS and CHB Using Restarts," CP 2021. Consulted
  for context on modern branching-heuristic combinations; not
  implemented directly. (`reports/REPORT7.md`)
- Marques-Silva, "The Impact of Branching Heuristics in Propositional
  Satisfiability Algorithms," 1999. Static/lexicographic variable
  ordering as a deliberate cheap baseline (`dfs`'s `--alg-params
  val1=1`). (`reports/REPORT6.md`)

## Restarts

- Luby, Sinclair & Zuckerman, "Optimal Speedup of Las Vegas
  Algorithms," 1993. The Luby restart sequence (`cdcl`'s
  `--alg-params val2=1`). (`reports/REPORT15.md`)
- Gomes, Selman & Kautz, "Boosting Combinatorial Search Through
  Randomization," AAAI 1998. General case for restarts in
  combinatorial search. (`reports/REPORT15.md`)
- Pipatsrisawat & Darwiche, on phase saving and component caching
  (2007-era MiniSat/RSat-lineage literature). Phase saving —
  remembering and reusing each variable's last-assigned polarity — is
  unconditionally on in `cdcl`. (`reports/REPORT14.md`)
- Audemard & Simon, "Predicting Learnt Clauses Quality in Modern SAT
  Solvers," IJCAI 2009 (Glucose). `cdcl`'s fourth restart strategy
  (`--alg-params val2=4`, previously `5` until `REPORT44.md` moved
  round-robin above it): restart when a moving average of recent
  learned-clause LBDs looks close to or worse than the all-time
  average, rather than on a fixed conflict-count schedule.
  (`reports/REPORT33.md`, `reports/REPORT34.md`)

**A note on this project's own restart-schedule naming**: `STAGE15.md`
originally asked for a "geometric" growth sequence specified as
`a·k²` — which is actually *quadratic* (polynomial), not geometric (a
geometric sequence has a constant ratio between consecutive terms).
`cdcl`'s `--alg-params val2=2` implements that quadratic sequence under
its correct name, "polynomial"; `val2=3` is a separately-added, true
geometric sequence (`c·rᵏ`, constant ratio `r`) — the schedule the SAT
literature actually calls "geometric restarts" (e.g. early MiniSat's
own restart scheme). See `reports/REPORT15.md` for the full story and
`docs/usage.md`'s `--alg-params` table for both.

## Preprocessing

- Eén & Biere, ["Effective Preprocessing in SAT Through Variable and
  Clause
  Elimination,"](https://www.cs.cmu.edu/~emc/15-820A/reading/een-biere-2005.pdf)
  SAT 2005 (SatELite). The general preprocessing approach (unit
  propagation, pure literal elimination, subsumption, and bounded
  variable elimination, all iterated to a fixpoint).
  (`reports/REPORT8.md`)
- Subbarayan & Pradhan, "NiVER: Non-Increasing Variable Elimination
  Resolution for Preprocessing SAT Instances," SAT 2004. The specific,
  simpler variable-elimination bound (never let elimination increase
  the clause count) `vibe_sat` actually uses, in place of SatELite's
  own more elaborate criterion. (`reports/REPORT8.md`)

## Parallel search and multi-threading

- Chase & Lev, "Dynamic Circular Work-Stealing Deque," SPAA 2005. The
  lock-free work-stealing deque `dfs`'s parallel search uses in Go
  (`go_src/internal/dfs/deque.go`) and, via the `crossbeam-deque`
  crate, in Rust. (`reports/REPORT18.md`, `reports/REPORT26.md`)
- Dijkstra & Scholten's termination-detection algorithm (background
  reading only — the actual protocol `dfs`'s parallel search uses is a
  simpler, centralized one, since this project runs as a single
  process with shared memory, not the distributed setting
  Dijkstra-Scholten targets). (`reports/REPORT16.md`,
  `reports/REPORT18.md`)
- Hamadi, Jabbour & Sais, "ManySAT: a Parallel SAT Solver." The
  continuous, lock-free, length-filtered clause-sharing design
  `cdcl`'s multi-threaded mode uses is modeled directly on this paper's
  approach (and its 8-literal length cutoff for shared clauses).
  (`reports/REPORT20.md`, `reports/REPORT21.md`)
- Le Frioux et al., "PaInleSS: a Framework for Parallel SAT Solving."
  Consulted alongside ManySAT for the shared-clause-pool design.
  (`reports/REPORT20.md`)
- "Community and LBD-Based Clause Sharing Policy for Parallel SAT
  Solving." Cited for the finding that clause sharing helps
  UNSAT-proving on random 3-SAT much more than it helps SAT-finding —
  which is exactly what `cdcl`'s own multi-threaded benchmarks showed.
  (`reports/REPORT20.md`)
- Wetzler, Heule & Hunt, "DRAT-trim: Efficient Checking and Trimming
  Using Expressive Clausal Proofs," SAT 2014. Surveyed as a possible
  future addition (machine-checkable UNSAT proofs); not implemented.
  (`reports/REPORT7.md`, `reports/REPORT22.md`)

## Benchmark problems

- Hoos & Stützle, [SATLIB Benchmark
  Problems](https://www.cs.ubc.ca/~hoos/SATLIB/benchm.html). The source
  of every file under `benchmark/`: the uniform random 3-SAT sets
  (`uf*`/`uuf*`), the "flat" graph-coloring instances, the
  blocks-world planning instances, and the DIMACS circuit-fault (`ssa*`)
  instances.
