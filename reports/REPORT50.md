# Report: Stage 50

## What this stage is

Per `STAGE50.md`, a third planning discussion, same format as
`REPORT22.md`/`REPORT33.md`: no code changes. I re-verified every item
on `REPORT33.md`'s 25-item backlog against reports 34-49 (most are
done; a few status changes are worth flagging), scanned reports 34-48's
own "Questions for you" sections for genuinely new items that never
made your list, folded in `STAGE50.md`'s three explicit instructions
(keep `REPORT49.md` item 12's SAT/UNSAT asymmetry question alive, don't
pursue a bigger Luby sweep, include `REPORT48.md`'s two further
opportunities), and added a handful of new ideas of my own given the
project's current state. Anything in `low_priority.md` (chronological
backtracking, DRAT/UNSAT proof logging, Option D for CDCL, XOR/Gaussian
elimination) is excluded entirely, per your instruction.

**My recommendation, if you want the short version**: `chooseWatch`
remembering its scan position across calls (below, Tier 1) is the
single best next item — it's a real, standard MiniSat-lineage
technique, directly continues the profiling story Stage 45/47/48
already told, and `chooseWatch` has been the dominant cost in every
recent `cdcl` profile. Bootstrap interruptibility and the progress
meter are both cheap, real reliability/UX fixes that have been sitting
unaddressed since Stage 35 and Stage 13/22 respectively.

## REPORT33.md's 25 items: what's still open

Verified against reports 34-49, not just guessed from titles. Eighteen
of the twenty-five are done (twelve resolved positively, three
resolved with an explicit negative/no-change finding, two done
"in spirit" or superseded by later work, one moved to
`low_priority.md`) — I'm not re-listing those; see the "Already
resolved" section at the bottom for the full accounting. What's
actually still open:

- **Lookahead solver** (a fifth algorithm) — still just as big and
  speculative as `REPORT33.md` left it.
- **Progress meter / heartbeat** — still unaddressed since
  `REPORT22.md` first sketched it.
- **Adaptive Novelty+** — still agreed low priority (`hc`/`ws` aren't
  the project's focus).
- **Expand the structured/industrial benchmark corpus** — still
  conditional; no specific need has arisen since `REPORT33.md` said to
  wait for one.
- **Import recent SAT conference papers** — still open, still
  recommended as its own narrowly-scoped stage rather than folded into
  anything else.
- **Thread BVE** — still low priority, unchanged from `REPORT33.md`'s
  own downgrade.
- **Per-thread `--verbose` breakdown** — still small and cosmetic.
- **Full benchmark sweep to exhaustion** — still background
  confidence-building, not a real priority.

## New items surfaced in reports 34-48, never acted on

- **`chooseWatch` remembering a scan position across calls
  (`REPORT41.md`/`REPORT47.md`).** A real, standard MiniSat-lineage
  technique: instead of always restarting the replacement-watch scan
  from a clause's first literal, remember where the last scan left
  off (per clause) and resume from there next time. `REPORT47.md`
  named this directly as the top candidate once `chooseWatch` was
  confirmed to be the dominant cost post-Stage-45; Stage 48 built a
  different, also-effective fix instead (the blocking-literal check)
  but never touched this one. Still fully open.
- **Bootstrap is not interruptible mid-flight (`REPORT35.md`).** Stage
  35's time-check fix (`REPORT33.md` item 2) covers the main search
  loop, but `UnitPropagate`/initial watch construction/
  `occurrence.Build` — everything that runs *before* the main loop
  starts — has no time-budget awareness at all. A single slow
  bootstrap call on a large or adversarial instance can still overrun
  `--time-limit-secs` with no way to interrupt it, the same class of
  bug item 2 fixed for the search loop itself, just in the one place
  that fix doesn't reach.
- **Binary resolution extension to clause minimization
  (`REPORT36.md`).** MiniSat's optional further refinement to
  self-subsuming clause minimization (Stage 36's own item 3 already
  implements the base technique); this specific extension was
  mentioned but never pursued.
- **A `paramtune` sweep over `rephaseIntervalRestarts`/
  `rephaseMaxFlips` before fully writing off WalkSAT rephasing
  (`REPORT43.md`).** Stage 43 implemented and benchmarked periodic
  rephasing (`REPORT33.md` item 6) and found it a near-tie, not a
  clear loss — but never tuned its own two parameters with the
  auto-tuning harness that now exists (it didn't yet, at Stage 43).
  Worth a genuine second look now that the tool exists and is
  well-exercised, before concluding rephasing just doesn't help here.
- **A dedicated multi-threaded benchmark of phase round-robin's actual
  diversity value (`REPORT43.md`).** Currently justified by analogy
  to `SelectVar`/restart-strategy round-robin, never independently
  measured on its own.
- **A fuller Kissat-style phase rotation (`REPORT43.md`).** Cycling
  saved/target/inverted/original phases across stabilizing-mode
  restarts, rather than the current simplified target-phase-only
  implementation (which measured clearly worse than plain phase-saving
  standalone). Bigger and more speculative than the item above.
- **A third round-robin portfolio dimension, or switching round-robin
  to random worker assignment (`REPORT44.md`).** Minor, speculative,
  explicitly framed at the time as "cross that bridge later" — no
  bridge has appeared since.

## From `STAGE50.md` directly

- **`REPORT49.md` item 12's open question — why does the reason-side
  signal help LRB's SAT solved-count so much more than its UNSAT
  solved-count?** You said to keep this as a possible project. It's a
  real, well-defined small investigation: compare `participated` vs.
  `reasoned` count distributions (and how often each actually differs
  in practice) across a SAT-heavy vs. UNSAT-heavy sample, and check
  whether the LRB paper's own SAT Competition benchmark mix was
  itself more SAT-solved-count-dominated than this project's uniform
  random 3-SAT set — that would suggest the asymmetry is about
  instance distribution matching the technique's original evaluation
  conditions, not something specific to this implementation.
  Understanding-focused, moderate effort, no guaranteed code change at
  the end of it.
- **`REPORT49.md` item 16's further Luby sweep — you said probably
  not**, reasoning that the six-file samples were likely sitting on a
  genuinely flat hilltop where pinning down the exact best value
  wouldn't be worth the data cost. I agree with that read: both
  samples agreed emphatically that `100` was wrong and that
  everything from `1000` to `8000` was in a broadly similar,
  meaningfully-better range — that shape (a wide plateau, not a sharp
  peak) is exactly the situation where more data narrows the
  confidence interval without changing the decision. Not re-listed as
  an active item below; recorded here as explicitly considered and
  declined, not overlooked.
- **`REPORT48.md`'s two further opportunities, per your instruction to
  include them in this stage's mix**:
  - **The append-on-watch-move cost** (~12% of total `propagate` time,
    not reallocation overhead, just the ordinary cost of a frequent
    operation). No fix was identified at the time beyond "a
    fundamentally different watcher-list data structure" — a much
    bigger undertaking than a quick win.
  - **`isFalse`/`Literal.Var()`'s combined ~20-32% share** — mostly
    the unavoidable cost of checking an assignment array by variable
    index; a real fix would need a more invasive representation
    change (e.g. packing sign into the assignment array's own
    indexing scheme).

  Both are included below (Tier 4) honestly: they're real, measured
  costs, but neither had an identified angle worth pursuing when
  `REPORT48.md` found them, and nothing has changed that since. Listed
  for completeness, not because I see a new opening.

## New ideas, not from any previous report

- **Bounded Variable Addition (BVA)** (Manthey, Heule & Biere,
  "Automated Reencoding of Boolean Formulas," 2012). BVE's natural
  complement: instead of *eliminating* a variable, BVA *introduces* a
  new one to replace a common literal pattern shared across many
  clauses, shrinking the clause count (sometimes substantially) on
  formulas with that kind of redundant structure — industrial/crafted
  instances more than uniform random 3-SAT, similar to BVE's own
  profile. `internal/preprocess` already has BVE's machinery (Stage 30/
  31) to build alongside; this would be a genuinely new preprocessing
  pass, not a tweak to an existing one.
- **Clause vivification.** A known technique (used by Lingeling/
  CaDiCaL among others; a preprocessing form is also described by
  Piette, Hamadi & Saïs, "Vivifying Propositional Clausal Formulae,"
  ECAI 2008): periodically re-derive and try to shrink *existing*
  clauses already in the database (not just freshly-learned ones, the
  way Stage 36's minimization already does) via extra propagation —
  if propagating a clause's own negated literals produces a conflict
  before reaching the last one, the clause can be shortened. A real,
  moderate-effort technique this project has no analogue of yet, since
  everything clause-shortening related so far (minimization) only
  touches a clause once, at the moment it's learned.
- **An incremental-solving / assumptions API** (MiniSat-style
  `solve(assumptions)`, reusing the learned-clause database across
  calls). This is the single largest idea on this list — a genuinely
  new capability, not an internal tuning change, and it's what real
  applications (bounded model checking, some verification workflows)
  actually need from a SAT solver rather than one-shot CLI solving.
  Flagging it because it's a real gap relative to what a "complete"
  solver usually offers, not because I think it belongs anywhere near
  the front of the queue — it would touch the CLI surface, both
  languages' public API, and probably deserves its own multi-stage
  arc if you're ever interested.

## The full backlog, ranked

### Tier 1 — do these first, real payoff, contained effort

1. **`chooseWatch` remembering a scan position across calls
   (`REPORT41.md`/`REPORT47.md`).** The best-value item on this list:
   a real, standard, well-understood technique, squarely aimed at the
   function every recent profile has named as the dominant cost, and a
   natural continuation of the Stage 45→48 story rather than a new
   direction.
2. **Bootstrap interruptibility (`REPORT35.md`).** Closes the one gap
   the time-check fix left open — a large/adversarial instance can
   still make `--time-limit-secs` a lie during bootstrap specifically.
   Contained (the bootstrap functions are already isolated), directly
   fixes user-visible reliability.
3. **Progress meter / heartbeat (`REPORT13.md`/`REPORT22.md`).** Cheap,
   real UX payoff, has been sitting unaddressed the longest of
   anything on this list (`REPORT22.md` first sketched it; you never
   responded either way).

### Tier 2 — real value, bigger or more open-ended

4. **`REPORT49.md` item 12's SAT/UNSAT asymmetry investigation.** Your
   own "keep as possible project" — see above for the concrete shape
   I'd give it.
5. **A `paramtune` sweep over `rephaseIntervalRestarts`/
   `rephaseMaxFlips` (`REPORT43.md`).** Worth resolving with the
   now-mature tuning harness before treating rephasing's "near-tie"
   result as final.
6. **Bounded Variable Addition (new idea, above).** Real literature-
   backed technique, natural companion to existing BVE machinery,
   plausible win on the same instance classes BVE already helps.
7. **Clause vivification (new idea, above).** Real technique this
   project has no analogue of; moderate effort, genuine potential
   payoff on the clause database as a whole rather than just
   newly-learned clauses.
8. **Binary resolution extension to clause minimization
   (`REPORT36.md`).** Smaller and more incremental than items 6-7, but
   cheap to add on top of already-working machinery.

### Tier 3 — worth doing, modest effort or modest payoff, more speculative

9. **Lookahead solver (fifth algorithm).** Unchanged from
   `REPORT33.md`: the single biggest implementation bet on this list,
   real but narrow payoff (wins on random/crafted instances, likely
   loses on industrial ones).
10. **A dedicated multi-threaded benchmark of phase round-robin's
    diversity value (`REPORT43.md`).** Closes a "justified by analogy
    only" gap; moderate effort, modest expected payoff since it's a
    measurement, not a change.
11. **Import recent SAT conference papers, scoped narrowly
    (`REPORT33.md` item 18, still unscoped).** Still recommend framing
    it as e.g. "survey the last 3 years of SAT Competition
    application-track winners' papers for techniques not on this
    list," rather than an open-ended literature dump.
12. **A fuller Kissat-style phase rotation (`REPORT43.md`).** Bigger
    and more speculative than item 10, given the simplified version
    already measured worse than plain phase-saving standalone.
13. **Per-thread `--verbose` breakdown (`REPORT22.md` item 19).**
    Small, cosmetic, data already collected — do it whenever
    convenient.

### Tier 4 — low priority, real but small, speculative, or no identified angle

14. **Adaptive Novelty+.** Unchanged: `hc`/`ws` aren't the project's
    current focus.
15. **Thread BVE (`REPORT25.md`).** Unchanged from `REPORT33.md`'s own
    downgrade.
16. **Expand the structured/industrial benchmark corpus.** Still
    conditional on a specific need that hasn't arisen.
17. **Full benchmark sweep to exhaustion.** Background
    confidence-building only.
18. **The append-on-watch-move cost, and `isFalse`/`Literal.Var()`'s
    combined share (`REPORT48.md`, included per your instruction).**
    Real, measured costs; no identified angle worth pursuing yet
    beyond "a fundamentally different data structure," which is a much
    bigger undertaking than either finding on its own justifies.
19. **A third round-robin portfolio dimension / random worker
    assignment (`REPORT44.md`).** Minor and speculative; still no
    concrete trigger to revisit it.
20. **An incremental-solving / assumptions API (new idea, above).**
    The largest single idea on this whole list; flagged for
    completeness, not urgency — would need its own multi-stage arc.

### Already resolved since `REPORT33.md` — not re-listed above

Items 1 (LBD-based clause management, Stage 34), 2 (time-check
interval, Stage 35 — see the bootstrap caveat above), 3 (learned
clause minimization, Stage 36), 4 (allocation reduction in
`analyze`/`addLearnedClause` — surveyed and measured negatively at
Stage 38: allocation share barely dropped and `GOGC=off` measured
*slower*, so explicitly not pursued further), 5 (smoke test in CI,
Stage 37), 6 (periodic rephasing, Stage 43 — implemented, a near-tie,
not adopted as default but a real option now), 7 (blocking-literal
check — Stage 48 built the unconditional version instead of the
length-gated one originally proposed, which now measurably wins), 9
(auto-tuning harness + `.vibe_sat.json`, Stage 39), 12 (LRB's omitted
refinements, Stage 49), 15 (parameter inventory doc, Stage 39), 16
(Luby restart base recalibration, Stage 49), 21 (standing Go-vs-Rust
harness — `util/benchcompare`, done in spirit per `REPORT33.md`
itself), 23 (a genuinely different watch-list representation — Stage
45/46's `watchersPositive`/`watchersNegative` rewrite is exactly this,
confirmed by `REPORT45.md`'s own opening framing), and 25
(`bveWorkBudgetFactor` tuning — measured negatively at Stage 39: no
value changes the outcome on the one pathological file it was aimed
at). Item 8 (chronological backtracking), 11 (DRAT/UNSAT proof
logging), 19 (Option D for CDCL), and the XOR/Gaussian elimination
item from `REPORT40.md` are all in `low_priority.md` and excluded
entirely per your instruction, not because anything changed about
them.

## Questions for you

- Tier 1's three items are all cheap-to-moderate and each closes a
  real, specific gap rather than chasing a diffuse improvement — does
  that ordering (watch-scan position, then bootstrap interruptibility,
  then the progress meter) match your own sense of priority, or would
  you reorder?
- Of the two new preprocessing ideas (BVA, clause vivification), both
  are real literature techniques this project has no analogue of yet
  — worth prototyping both eventually, or do you want me to pick one
  to scope first?
- The incremental-solving/assumptions API is the biggest idea on this
  list by far and touches the CLI surface in both languages — is that
  a direction you're actually interested in, or was it worth
  mentioning once and then leaving alone unless something changes?
