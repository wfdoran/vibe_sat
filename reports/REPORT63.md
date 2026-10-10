# Report: Stage 63

## What this stage was

Per `STAGE63.md`: no code. A literature survey of the last three years of
SAT Competition papers, looking for techniques this project has not already
discussed. The solvers did not have to win, only to have done well. Anything
in `low_priority.md` (chronological backtracking, DRAT, Option D, XOR/Gauss)
is excluded, and so is everything already on `todo.md` or `REPORT50.md`'s
list, except where the new papers add something concrete to it.

**Scope.** I took "the last three years" as the competitions of **2024, 2025
and 2026** (all three have published results), and also read the **2023**
material, because the 2023 winner's technique (SBVA) is the one this project
just built. Nothing was changed in the repo except this report.

## 1. How I did it, and what I could and couldn't read

**Read in full (the text itself):**

- SAT Competition 2025 *Proceedings of Solver and Benchmark Descriptions*
  (all solver entries, pages 8 to 32).
- SAT Competition 2023 proceedings (the entries I list in section 4).
- Biere, Faller, Fazekas, Fleury, Froleyks & Pollitt, *CaDiCaL, Gimsatul,
  IsaSAT and Kissat Entering the SAT Competition* **2024** and **2025**.
- The SBVA paper (Haberlandt, Green, Heule, SAT 2023), the 3-hop heuristic
  and results sections.
- The 2025 and 2026 *results slides* (rankings).

**Read only as abstracts or summaries** (so I quote their authors' claims, and
did not check the details): *Simplify, Order, Break, Repeat* (SAT 2026),
*Revisiting Restarts of CDCL* (2024), *Extended Resolution Clause Learning via
Dual Implication Points* (2024), *Rethinking Clause Management for CDCL SAT
Solvers* (2026), the SATLUTION paper (2025), and the LymphoSAT write-up (2026).

**Not found or not read:** the 2026 solver-description proceedings (I could not
locate them), so for **Kissat-MAB-HyPre, EDA-Kissat and AE-Kissat-HUCB I know
only names, rankings and a one-line summary**; and the non-Freiburg entries in
the 2024 proceedings (the 2024 proceedings page still said "tbd"). The
`curl` command was blocked by the sandbox, so all of this came through the
web tools.

Everything below that is marked **(my knowledge)** comes from my own background
knowledge of the field, not from the papers I just read.

## 2. What won, in three lines each

| Year | Main-track result | What was behind it |
|---|---|---|
| **2023** | 1st overall: **SBVA-CaDiCaL** (PAR-2 3274, 284 solved); SAT-only: KissatMabProp; UNSAT-only: SBVA-Kissat | A *preprocessor* (structured bounded variable addition) plus a bandit-tuned Kissat |
| **2024** | **Kissat** won SAT, UNSAT and SAT+UNSAT; the runners-up were "far behind" | A pile of new *inprocessing* (section 3) and better scheduling |
| **2025** | SAT: **AE-Kissat-MAB** (PAR-2 716); UNSAT: **CaDiCaL-SC2025**, then Kissat-VSA and AE-Kissat-bump | LLM-assisted heuristic tweaks to Kissat (section 5.7); CaDiCaL caught up by porting Kissat's techniques |
| **2026** | 1st overall: **satsuma-iter+kissat** (276 solved, PAR-2 3647); 2nd Kissat-MAB-HyPre (255); 3rd EDA-Kissat-v2 (240). Separate **AI-generated** sub-track: **LymphoSAT** | *Static symmetry breaking in front of Kissat*; bandits again; AI-written solvers |

Two patterns stand out. **Every winner is Kissat or CaDiCaL underneath**, so
the wins come from techniques bolted on, not from a different core. And the
competition has become heavily **AI-assisted**: in 2026 twelve of the 33
sequential submissions were AI-generated or AI-tuned.

## 3. What Kissat did in 2024 (the clearest list of techniques)

From the 2024 description (these are the author's own words, condensed):

- **Dynamic tier limits for learned clauses.** Instead of the fixed "keep glue
  at most 2 forever, glue at most 6 for a while" thresholds, count how many
  clauses of each glue were actually *used* in conflict analysis, and set the
  tier-1 limit at the glue covering 50% of used clauses and tier 2 at 90%.
  Hard-coded values (2 and 6) are the starting point.
- **Revisited vivification**: tiers vivified separately within a per-tier
  propagation ("ticks") budget, then irredundant clauses with leftover budget;
  redundant clauses that are asymmetric tautologies are dropped eagerly. (This
  is `REPORT50.md` item 7; see section 6.)
- **Clausal congruence closure** (gate extraction plus congruence closure) and
  **clausal equivalence sweeping** (a small embedded SAT solver, "Kitten",
  finds equivalent literals), **binary backbone extraction**, and a **fast
  bounded variable elimination** (with forward subsumption, no gate
  extraction) in the initial preprocessing round.
- **Lucky phases**: before searching, try a few trivial assignments with
  propagation (all false, all true, forward, backward), extended with a
  look-ahead variant.
- **Ticks-based scheduling**: inprocessing is scheduled by an estimate of
  propagation cost, not by conflicts or time; stable and focused mode are also
  interleaved "more precisely" by ticks.
- Binary-reason jumping kept only for formulas with over 100,000 clauses, more
  than 99% of them binary (the author reports it generally hurt).

Also notable: Kissat's **BVA** has the SBVA tie-breaking heuristic *turned off*,
because "it only helped on a few benchmarks but overall gave worse results".

## 4. Ideas new to this project, ranked

How I ranked them: expected benefit on *this project's* benchmarks, effort in
two languages, and risk. "Connects to" points at something we have actually
measured. Effort is a guess.

### Tier 1: cheap, plausible, measurable on what we already have

**1. Stable/focused mode switching (Kissat's core architecture).** Kissat
alternates between two search modes inside one run: a *focused* mode (VMTF
variable ordering, fast Glucose-style restarts) and a *stable* mode (VSIDS,
slow restarts, target phases), switching on a growing schedule. Neither is a
new idea (Chanseok Oh, SAT 2015, is the cited origin), but this project has none
of it, and every winner above uses it. It connects directly to what we measured:
Stage 59 found `target` phase is best on SAT and worst on UNSAT, and today we
get that mix by running *different threads* with different strategies. Mode
switching gets both in one thread. It would also need **VMTF**, which we do not
have (a cheap structure: a linked list moved to the front on bump). Effort:
moderate. Payoff: plausibly the largest single item here for the 1-thread
default.

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

**3. Cold restarts.** Zhang, Chen & Cai (2024) distinguish *warm* restarts (what
we do: drop the assignment, keep order, phases and learned clauses) from *cold*
ones that also forget some of that: the branching order, the phases, or part of
the learned clauses. They report benefit "particularly on satisfiable
instances" (abstract; no numbers seen). Kissat-CURE (2025) and the 2025
MAB solvers implemented these policies. Our restart framework already has hooks
for periodic actions (rephasing), so an experiment is small. It also targets the
SAT-side weakness `REPORT59.md` found.

**4. Lucky phases.** A handful of trivial full assignments checked by
propagation before the search begins; cheap, and it can make some crafted SAT
instances instant. Small expected payoff on `benchmark/`, near-zero cost.

**5. Reason-side bumping for VSIDS, with a ticks limit.** `REPORT49.md` found
LRB improved a lot on SAT when reason-side bumping was added. Kissat and IsaSAT
both bump the literals of the *reason* clauses too, with a propagation-cost limit
to keep it cheap (the IsaSAT note says the limit mattered on a low-conflict-rate
family). We have only done this for LRB. A small tweak and a quick sweep.

### Tier 2: moderate effort, real payoff where it fits

**6. Static symmetry breaking in front of the solver (Satsuma-style).** This is
the **2026 main-track winner**: `satsuma-iter+kissat` solved 276, 21 more than
second place. It also won the UNSAT-only category (149 solved against 121 for
second place), and a Satsuma variant on a different Kissat fork
(`satsuma-iter+ae-kissat-mab`) took first in the SAT-only category among the
non-AI solvers. The 2026 paper *Simplify, Order, Break,
Repeat* (abstract) reports a **22% PAR-2 improvement on the 2025 competition
benchmarks** and 12% on the anniversary track. The idea: instead of the
traditional lex-leader constraints that restrict the search, add only **unit and
binary** symmetry-breaking clauses, so they also *simplify* the formula, which
exposes further symmetries, and repeat. It connects to our one hard structured
family: **pigeon-hole** (`hole10` is 48 s single-threaded and gets worse with
threads, `REPORT61.md`) is the textbook symmetric problem. The catch is the
detection step: Satsuma finds row-interchangeability, row-column, and Johnson
symmetries via graph-automorphism machinery. Writing that from scratch in two
languages, with no external packages in Go, is the real cost (large), unless we
implement only the simplest pattern (interchangeable rows, i.e. the pigeon-hole
shape) first (moderate). Reeves & Bryant's **PreLearn** (2023) reaches the same
place a different way: it finds short **propagation-redundant (PR)** clauses
with a SAT solver, which "can resemble symmetry-breaking". PR clauses preserve
satisfiability but not equivalence, which would be fine for us (a model of the
extended formula is a model of the original). It needs a solver call per
candidate, and proof tooling we don't need.

**7. Cardinality detection and re-encoding.** Reeves & Bryant's **ReEncode**
(2023) finds at-most-one constraints (pairwise ones by greedy clique growth on
binary clauses, others by guessing and BDD verification) and re-encodes them
with the commander encoding. Reeves' 2025 *unique literal clauses* module
detects exactly-one constraints in linear time and re-encodes them as a
sequential counter or order encoding, with an "alignment" step to keep the
encodings consistent. This is a *targeted* cousin of the BVA we built:
`REPORT61.md` found BVA shrinks pigeon-hole by 40 to 45% but the solve time was
erratic (hole9 3x slower, hole10 up to 3x faster), and the SBVA paper (below)
gives the likely reason. An at-most-one-aware encoding is the principled version
of what BVA found by accident there. Effort: moderate.

**8. Making BVA less chaotic: SBVA's 3-hop tie-breaking, and what the
competition found.** The SBVA paper shows exactly the instability we saw: BVA
"relies on variable order to break ties", so *randomizing the variable order
before BVA* loses its benefit even when the formula shrinks by the same amount.
Their fix: break ties by the number of paths of length 3 in the *variable
incidence graph* (computed with sparse-matrix products), so BVA adds variables
for things that are close together in the problem's structure. On more than
29,000 formulas this recovered the loss from randomization and did better than
plain BVA. Our implementation breaks ties by literal code, which is precisely
the fragile choice. Cautions: Kissat's own authors *disabled* it as it gave
worse results overall (section 3), and in the paper, "even formulas reduced to
less than 10% of their size may be slowed down". So this is only worth doing if
BVA is to be turned back on. A related 2025 entry (*Activity-Guided Dynamic BVA*)
runs BVA at restarts during search, choosing literals by variable activity and
avoiding merges of variables with very different activity.

**9. Failed-literal probing, equivalent-literal substitution, and binary
backbones.** Kissat's 2024 initial round runs congruence closure, binary
backbone extraction and equivalence sweeping before a fast BVE; AMSAT (2023)
adds failed-, implied- and equivalent-literal detection. The classic building
blocks are (my knowledge) the strongly connected components of the
binary-implication graph (equivalent literals) and one-literal look-ahead at
decision level 0 (failed literals). None of this is in the project: we only
preprocess once, with unit propagation, pure literals, subsumption and BVE.
`REPORT7.md` and `REPORT41.md` mention "inprocessing" in general, but never
these specific techniques, and `REPORT33.md` mentions failed literals only in
the context of a lookahead solver. Gate-based congruence closure and Kitten-style
sweeping (the 2024 headline features) aim at hardware-miter instances and are
large; the cheap subset (SCC equivalences, failed-literal probing) is moderate.

**10. Parallel design changes from MallobSat "naps" (2025).** The 2025 parallel
winner is explicit that it is "not a portfolio solver" (hence *naps*):

- **60 uniform Kissat threads**, with *all* pre- and inprocessing off and *no*
  per-thread strategy cycling; the only diversification is different random
  seeds and sparsely randomized phases, with clause sharing acting as search
  pruning.
- **Four local-search (YalSAT) threads** as independent "needle in the haystack"
  searchers.
- **A sequential preprocessor running while the other threads already solve the
  original formula**, which they replace with the preprocessed formula when it
  finishes.
- They also *dropped* LBD-based clause-sharing logic by resetting every shared
  clause's LBD to its maximum, reporting no performance loss.

This is a direct comparison to our Option B: our round-robin of restart and phase
strategies is exactly the diversification MallobSat removed, and `REPORT59.md`
found its benefit weak. The cheap piece for us is the second bullet: we already
have WalkSAT (`ws`), so running a few of them as extra workers inside `-z N`
`cdcl` is a small change. Also relevant: `REPORT61.md` found `-z 8` *slower* than
one thread on `hole10`.

### Tier 3: research-flavored, speculative, or meta

**11. Bandit-controlled heuristics (the "MAB" lineage).** The names in the
winners' lists say it all: Kissat-MAB-* in 2023, 2025 and 2026, Kissat-MAB-HyPre
2nd in 2026, AE-kissat-HUCB 3rd in the 2026 SAT category. The common idea is a
multi-armed bandit (UCB, Thompson sampling) that picks *at each restart* among
candidates: VSIDS versus CHB, one of several rephasing heuristics
(`Kissat_MAB_CoRephase`), or a whole configuration (`Kissat-CURE`'s "dynamic
configuration tuning", `DynamicCaDiCaL`). `REPORT60.md` showed several of our
parameters matter by instance family (for example `lrbAlpha` 0.4 left 9 to 10
of 24 files unsolved, 0.1 solved all), which is the situation bandits are meant
for. We already have both VSIDS and LRB, so a restart-time UCB switch between
them is a modest experiment. I am not recommending the heavier whole-config
version.

**12. Software prefetching and cache layout in propagation (Kissat-INCSP,
2023).** The authors profiled Kissat, found `propagate_literal` the top hotspot
(the first watch-list entry and the non-binary clauses it touches are cache
misses), and added software prefetch of the *next* watch list's head, plus a
cache-friendly copy of the VMTF queue (they report a 600 s improvement on one
benchmark for that part; I did not find a clean number for the prefetch part).
For us it is a **language asymmetry**: Rust exposes prefetch intrinsics
(`core::arch`), Go has no portable prefetch. That makes it an unusual,
project-fitting experiment (it is the kind of Go-versus-Rust difference
`PROMPT.md` says is part of the point), and our profile says propagation is
where the time goes (`REPORT48.md`/`REPORT51.md`). Speculative: it is a
micro-architecture optimization whose effect I can't predict.

**13. LLM-assisted heuristic evolution.** Two different papers apply this:

- **AE framework (2025 winner):** an LLM pipeline mutated Kissat's variable
  *bump increment* (a piecewise dynamic decay with a compensation factor) and
  its *score rescaling* (switching among three scaling formulas by a pseudo-
  random choice); the authors report about **5% PAR-2 improvement** for the bump
  change and about **3%** for a tweak to the bandit's exploration term, on the
  2024 benchmarks.
- **SATLUTION (NVIDIA/Maryland, 2025):** an agent evolving a whole C/C++
  solver repository from five 2024 solvers; the authors report solved counts of
  347, 345 and 344 against 334 and 331 for the 2025 gold and silver winners.
  These are the authors' claims and I did not check them.

This project is itself LLM-written, and it already has the harness
(`util/paramtune`, `benchcompare`, a correctness checker) that this style of
loop needs, so a small trial is possible: for example, let an agent propose
changes to *one* function (VSIDS bump/decay, or the clause-reduction policy)
and let `paramtune` judge. Caveats: it needs a good, large benchmark set to
avoid overfitting, our parameter sweeps (`REPORT60.md`) already showed
differences under about 10% are noise at our sample size, and tweaks this small
would not be measurable here.

**14. Satisfiability prediction (Kissat-Pred, 2025).** Extract linear-time graph
features (degree statistics of the clause, variable, and clause-variable graphs)
after preprocessing and use a trained decision tree to predict SAT or UNSAT, then
pick SAT-oriented or UNSAT-oriented search settings. Given our SAT/UNSAT
asymmetries (target phase, LRB), a prediction could drive them. It needs a
trained model and a benchmark corpus to train on; I'd skip it.

**15. Extended resolution during search (Dual Implication Points, 2024).** A
generalization of unique implication points to *pairs* of dominators in the
implication graph; the solver introduces a new variable defined for each pair.
Implemented in MapleLCM ("xMapleLCM"), reported better than Kissat 3.1.1,
CryptoMiniSat 5.11 and SBVA+CaDiCaL on Tseitin and XORified formulas (abstract
only). That is a strength on XOR-like instances you have said are not an
interest, so I rank it low.

**16. Priority-based (delayed) BCP (MapleCaDiCaL, 2023).** Changes the order in
which propagated literals are processed, using VSIDS as the priority, switched
adaptively by a Thompson-sampling bandit. It changes which conflicts are found
first. I'd treat it as research only.

## 5. Items I looked at and would not pursue

- **LymphoSAT (2026 AI sub-track winner, 146 solved).** It is 126
  family-specific solvers generated by AI agents plus detectors that route a
  formula to the right one, with Kissat as the fallback (author: roughly $10,000
  of LLM spend and $5,000 of cloud). The author himself notes the competition
  format favors it, since about half of the test formulas come from historical
  families it was specialized to. It is an impressive result and I'd not copy it:
  it is the opposite of building one understandable solver.
- **CNF-to-AIG and the dilemma calculus (2025 `dilemma` solver),** and
  hardware-miter techniques in general (congruence closure on gates): they target
  circuit-derived instances; our `bf`/`ssa` files are tiny.
- **SATLUTION-style whole-repo evolution** (as opposed to the one-function trial
  in item 13): too large for this project's scale.

## 6. Refinements to items already on your list

- **Clause vivification (`REPORT50.md` item 7).** The new papers add concrete
  design: vivify tiers separately within a **propagation-cost budget**; spend
  leftover budget on the next tier; try *irredundant* clauses at the end; drop
  redundant clauses that propagation shows implied (asymmetric tautologies) eagerly.
  The 2025 entry *Kissat_VSA* orders the clauses to vivify by the **highest
  minimum variable activity**, only in stable (VSIDS) mode; it placed 2nd in the
  2025 UNSAT category.
- **BVA (done, off by default).** Item 8 above, plus the 2023 observation that
  *compression factor does not predict speed-up*: even with a large reduction,
  "the speedup is much more variable". That matches our measurements.
- **Unit-propagation worklist (todo).** No new information.

## 7. Suggested order, if you want one

1. **Scheduled clause reduction with tiers** (item 2). It has a measured 3x
   signal in our own data, and the answer to the carry-over memory-limit question.
2. **Stable/focused mode switching with VMTF** (item 1). Biggest structural gap
   against every winner.
3. **Cold restarts + reason-side bumping + lucky phases** (items 3, 4, 5) as one
   small "cheap tweaks" stage, each measured with the existing harness.
4. **Pigeon-hole-oriented work** (items 6, 7), if you want the structured/hard
   family to improve: re-encoding first (moderate), symmetry breaking second (the
   2026 winner, but a large detection effort).
5. **WalkSAT workers inside parallel `cdcl`** (item 10) and a **VSIDS/LRB bandit**
   (item 11), as parallel/heuristic follow-ups.

## 8. Caveats

- **Competition summaries are the authors' own and non-peer-reviewed** (the
  proceedings say so). Their reported gains (3%, 5%, 22%, "600 s on one
  benchmark") are *theirs*, on *their* benchmark sets.
- **I could not read the 2026 descriptions**, so I cannot say what HyPre or
  EDA-Kissat actually do. The search summaries said "hybrid preprocessing" for
  HyPre, which I do not take as reliable. The 2026 symmetry-breaking result is
  from the slides and an abstract.
- **Competition benchmarks differ from ours.** The SAT Competition main track is
  industrial, 400 instances, 5000 s limit; ours is mostly small random and
  crafted. Wins that depend on huge hardware-verification instances (congruence
  closure, sweeping) may not show up here at all. That is why I ranked by
  fit to *our* measured behavior, not by how prominent a technique was.
- **Rankings in section 4 are my judgment**, and their effort estimates were
  not measured.

## 9. Sources

Competition results and proceedings:

- [SAT Competition 2023 results](https://satcompetition.github.io/2023/results.html)
- [SAT Competition 2024 medals (Freiburg)](https://cca.informatik.uni-freiburg.de/sat24medals/)
- [Kissat triumphs in the SAT 2024 competition (Freiburg news)](https://news.vm.uni-freiburg.de/en/newsarchive/kissat-triumphs-in-the-sat-2024-competition)
- [The Results of SAT Competition 2025 (slides)](https://satcompetition.github.io/2025/satcomp25slides.pdf)
- [The Results of SAT Competition 2026 (slides)](https://satcompetition.github.io/2026/downloads/satcomp26slides.pdf) and the [2026 competition page](https://satcompetition.github.io/2026/)
- [Proceedings of SAT Competition 2025: Solver and Benchmark Descriptions](https://repositum.tuwien.at/bitstream/20.500.12708/218424/2/Codel-2025-Proceedings%20of%20SAT%20Competition%202025%20%20Solver%20and%20Benchmark%20Desc...-vor.pdf)
- [Proceedings of SAT Competition 2023: Solver, Benchmark and Proof Checker Descriptions](https://helda.helsinki.fi/server/api/core/bitstreams/6c4161a1-6889-4503-a366-2e0dd25b99a7/content)
- [CaDiCaL, Gimsatul, IsaSAT and Kissat Entering the SAT Competition 2024](https://cca.informatik.uni-freiburg.de/papers/BiereFallerFazekasFleuryFroleyksPollitt-SAT-Competition-2024-solvers.pdf)
- [CaDiCaL, Gimsatul, IsaSAT and Kissat Entering the SAT Competition 2025](https://cca.informatik.uni-freiburg.de/papers/BiereFallerFleuryFroleyksPollitt-SAT-Competition-2025-solvers.pdf)
- [KIT: MallobSat scores 1st in SAT Competition 2025](https://satres.kikit.kit.edu/news/2025-08-15-satcomp/)

Papers:

- [Effective Auxiliary Variables via Structured Reencoding (SBVA), SAT 2023](https://arxiv.org/abs/2307.01904)
- [Clausal Congruence Closure, SAT 2024](https://drops.dagstuhl.de/entities/document/10.4230/LIPIcs.SAT.2024.6)
- [Simplify, Order, Break, Repeat, SAT 2026](https://drops.dagstuhl.de/entities/document/10.4230/LIPIcs.SAT.2026.4)
- [Satsuma: Structure-Based Symmetry Breaking in SAT](https://arxiv.org/pdf/2406.13557)
- [Revisiting Restarts of CDCL: Should the Search Information be Preserved?](https://arxiv.org/abs/2404.16387)
- [Extended Resolution Clause Learning via Dual Implication Points](https://arxiv.org/abs/2406.14190)
- [Rethinking Clause Management for CDCL SAT Solvers](https://arxiv.org/abs/2602.20829)
- [Autonomous Code Evolution Meets NP-Completeness (SATLUTION)](https://arxiv.org/abs/2509.07367)
- [LymphoSAT: domain-specific hyperspecialization for SAT](https://c.mov/lymphosat/)

## 10. Notes

- No source code, tests or docs were changed this stage. I created scratch files
  in the scratchpad only (PDF text extractions); nothing in the repo besides this
  report.
- No decision is needed from you this stage; the suggested order in section 7 is
  only a suggestion for the next one.
