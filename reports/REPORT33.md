# Report: Stage 33

## What this stage is

Per `STAGE33.md`, no code changes: a second planning discussion, same
format as `REPORT22.md`. I re-read `REPORT22.md` through `REPORT32.md`'s
"Open questions / notes for you" sections (`REPORT22.md` itself already
covered `REPORT1.md`-`REPORT21.md`), found several real items that never
made it onto your list, researched every open item (including the four you
asked about directly), and ranked everything by bang-for-buck. One
housekeeping note first: **your "LRB" section's body text is an accidental
duplicate of the Chronological Backtracking section above it** — both
quote `REPORT22.md` item 15 verbatim. The real item 16 (LRB's omitted
refinements) is below, researched under its correct heading.

**My recommendation, if you want the short version**: two items stand out
as genuinely high-value and comparatively cheap — **LBD-based clause
management** (a real, well-established, currently-missing CDCL technique,
not on anyone's list until now) and **the time-based time-check interval**
(`REPORT29.md`'s own recommendation, still unaddressed, and now a
*confirmed reliability bug*, not just a guess). I'd sequence those before
anything bigger. The smoke-test-in-CI item is nearly free and worth doing
regardless of what else you pick.

## Answering your four direct questions first

### Lookahead solvers: does the lookahead cost pay for itself?

It depends heavily on instance class, and the literature is fairly clear
about *which* class: lookahead solvers (OKsolver, `satz`, `kcnfs`,
`march_ks` — repeated SAT Competition category winners) spend real,
non-trivial cost per node — for every candidate variable, tentatively
assign both polarities and run limited propagation to see how much each
choice simplifies the formula (or detects a "failed literal," an implicit
unit propagation to a contradiction) — which is `O(vars)` propagation
passes per node instead of CDCL's `O(1)`-ish decision. That cost reliably
pays for itself, often dramatically, on **random k-SAT and many crafted/
combinatorial instances** (pigeonhole-family, Ramsey-type, some
cryptographic and combinatorial-design encodings) — deep, exhaustive
failed-literal detection prunes far more of the tree than a cheap decision
heuristic ever could, on instances with little exploitable clause
structure. It reliably does **not** pay for itself on industrial/
application instances, which is exactly why CDCL (not lookahead) has
dominated the SAT Competition's application track for two decades: clause
learning captures structural information across the whole search that
lookahead's per-node local reasoning doesn't, and modern industrial
instances are usually large enough that `O(vars)`-per-node lookahead becomes
the dominant cost with no compensating tree-size win.

Given this project's benchmark mix now spans both ends (SATLIB's random
`uf`/`uuf` sets, and Stage 29's real SAT Competition 2018 corpus,
`blocksworld`, `flat`, `ssa`), a lookahead solver would likely win clearly
on the former and lose clearly on the latter — a genuinely interesting
fifth algorithm and a real new data point for the Go-vs-Rust comparison,
but the largest single implementation effort on this whole list (comparable
to standing up `dfs`+BCP or `cdcl` from scratch, in both languages).

### CDCL/local-search hybrid: what I'd actually build

Not what you sketched ("local search drives the complete CDCL search") —
that's a much bigger, riskier architecture than the literature suggests you
need. What real modern solvers do instead, and what I'd recommend here, is
**periodic rephasing**: every so often (every N restarts, or N conflicts),
run a short WalkSAT burst on the current formula and copy its resulting
assignment into CDCL's saved-phase array (`savedPhase`, already built in
Stage 14) instead of letting phase-saving alone drive polarity choices.
This is a real, current technique — CaDiCaL and Kissat (SAT Competition
winners since 2018-2019) both do exactly this, sometimes alternating
between phase-saving, a fixed pattern, and a local-search-derived phase
across restarts. It's a much smaller lift than a full hybrid architecture
because it reuses two things this project already has (`ws`'s WalkSAT loop,
`cdcl`'s phase-saving array) rather than designing new machinery, and it's
easy to A/B test in isolation. This is my concrete answer to "belief/survey
propagation" too: those are real (Mézard-Parisi-Zecchina survey
propagation solved large random k-SAT instances near the satisfiability
threshold that nothing else could touch in the early 2000s), but they're a
much larger, more research-grade undertaking (a genuinely different
message-passing algorithm, plus decimation-and-fallback logic) for a
benchmark class — huge near-threshold random k-SAT — this project's own
benchmark set doesn't actually emphasize. I'd treat it as interesting but
low priority relative to its cost here, unless you specifically want to go
find or generate that class of instance.

### Chronological vs. non-chronological backtracking

Standard CDCL (what `cdcl` already does) is **non-chronological**: on a
conflict, `analyze` computes a learned clause and finds the second-highest
decision level among its literals, then jumps *directly* to that level —
potentially undoing many decision levels at once, however far back that
turns out to be. This is the "C" in CDCL's own name's spirit, and it's most
of why CDCL escapes bad parts of the search tree so much faster than plain
DPLL backtracking one level at a time.

**Chronological backtracking (Nadel & Ryvchin, SAT 2018)** is the
counter-intuitive finding that *always* jumping to the theoretically-best
level isn't actually best in practice: sometimes backtracking just **one**
level — even when a much shorter non-chronological jump is available — does
better, because it keeps the trail more stable in ways that interact well
with phase-saving and activity decay, and avoids some pathological
"jump-then-immediately-conflict-again" patterns non-chronological jumping
can fall into. Their paper proposes backtracking chronologically
specifically when the non-chronological target would be close anyway (a
heuristic threshold), falling back to standard non-chronological jumps
otherwise. It's real and adopted (CaDiCaL, Kissat both implement it), but
it's a genuine correctness-sensitive change to `cdcl`'s core backtracking —
the paper spends real space on how to keep the trail's invariants valid
when some later-assigned variable ends up depending on an earlier one that
chronological backtracking left in place out of decision-level order.

### `SelectVar`'s literal-combination function: real literature, and I'd try one

Yes — this maps directly onto a known split in classical DPLL branching
heuristics. What `dfs.go`'s `SelectVar` already does (`score[v] += weight`
for every unassigned variable in an unsatisfied clause, regardless of the
literal's sign) is mathematically `F(a,b) = a+b`, where `a`/`b` are the
variable's positive/negative occurrence weights — this is the **two-sided
Jeroslow-Wang** family (Jeroslow-Wang itself weights each clause
`2^-length` rather than `0.7^(n-2)`, but the combination rule, "add the two
sides," is the same idea). Your candidate `F(a,b) = (a+1)*(b+1)` is exactly
the **MOM's heuristic** family (Maximum Occurrences in clauses of Minimum
size, e.g. Freeman's 1995 thesis and subsequent DPLL literature): the
intuition is that a variable useful to branch on should score well on
*both* polarities, not just have a large combined total — a lopsided
variable (`a=10, b=0`) scores low under multiplication despite a high sum,
while a balanced one (`a=5, b=5`) scores high, because branching on it is
likely to produce short/unit clauses whichever way you set it.
`min(a,b)`/`max(a,b)`/`2·min+max` are also real, documented variants in the
same family, trading off "guarantee some minimum benefit" against "reward
the best case."

One implementation wrinkle worth flagging: `dfs`'s `step` already tries
`False` then `True` unconditionally for whichever variable `SelectVar`
picks — the heuristic currently has no say over *polarity*, only over
*which variable*. That actually makes this cheaper to experiment with than
it might look: you only need to track `a`/`b` separately per variable
during the existing clause scan (instead of merging them into one number as
soon as they're found) and apply your chosen `F` once at the end, no
polarity-selection logic needed. Small, isolated, and — this is the
appealing part — cheap to A/B test empirically against the current `a+b`
on the benchmark set you already have, rather than needing to guess which
`F` is right from theory alone.

## The full backlog, re-scanned and re-ranked

### Tier 1 — do these first, real payoff, contained effort

1. **LBD-based clause management (Glucose-style), new idea.** Not on your
   list, and I think it's the single best-value item here. "Literal Block
   Distance" (Audemard & Simon, 2009) scores a learned clause by how many
   distinct decision levels its literals span — a low LBD means the clause
   is "compact" (likely to be reused, worth keeping); using LBD instead of
   (or alongside) pure activity to drive both the clause-deletion policy
   and restart triggers is one of the two or three most consequential CDCL
   ideas after watched literals and VSIDS itself, and essentially every
   competitive solver since Glucose (2009) uses some form of it. `cdcl`
   currently has none of this — I checked `cdcl.go` directly this stage:
   clause deletion (`reduceClauseDatabase`) and restarts are both driven
   purely by MiniSat-style clause/variable activity decay, no notion of
   LBD anywhere. Moderate effort (compute LBD once per learned clause,
   thread it through the existing clause-database/eviction and restart
   logic already built in Stages 11-15), real literature-backed payoff.
2. **Time-based time-check interval, `REPORT29.md`'s own recommendation.**
   Missing from your list entirely. `dfs`/`cdcl` both check the wall clock
   only every 4096 nodes/conflicts — fine when nodes are cheap, but
   `REPORT29.md` measured this blowing a 3-second budget to 42.6 seconds
   (a 14x overrun) on a large real instance, and `REPORT32.md`'s own
   scaling investigation ran into the same family of issue again. This is
   no longer a hypothetical: it's a confirmed reliability problem with
   `--time-limit-secs`, the flag users actually depend on to bound a run.
   Low-to-moderate effort (check elapsed wall-clock time directly instead
   of a node-count proxy, with some care to keep the check itself cheap —
   e.g. only calling the clock once every K nodes but with K small enough,
   or checking unconditionally and confirming empirically it's cheap
   enough not to matter), directly fixes user-visible behavior.
3. **Learned clause minimization, new idea.** Also not on anyone's list.
   Recursive/self-subsumption minimization of a freshly learned clause
   (removing literals already implied by the clause's other literals via
   the implication graph — MiniSat has done this since 2005) typically
   shrinks learned clauses by a real, measurable margin with a cheap,
   well-specified algorithm. It directly helps item... see below.
4. **Reduce allocation in `analyze`/`addLearnedClause` (`REPORT23.md`
   "bigger possible changes" item 2 — missing from your list).**
   `REPORT23.md`'s own memory profile found these two functions account
   for **~91% of all heap allocation** in an 8-thread `cdcl` run,
   independent of clause sharing. `REPORT23.md` flagged the naive fix
   (a simple object pool) as unsafe given learned clauses' genuinely
   indefinite lifetime, and recommended real reference-counting or
   epoch-based reclamation instead — a bigger design exercise, but shorter
   learned clauses (item 3, above) reduce the *size* of the problem for
   free before anyone builds the harder allocation-scheme fix, so I'd
   sequence 3 before 4. This is also a nice one for the Go-vs-Rust
   comparison specifically: Go's GC is far more allocation-sensitive than
   Rust's ownership model is here, so a fix aimed at Go's GC pressure might
   show a real, measurable *language-specific* asymmetry worth reporting
   on its own.
5. **Smoke test in CI, your new idea.** Genuinely cheap: a handful of small
   `benchmark/` files run through the actual CLI (both languages, a couple
   of flag combinations) added to the existing GitHub Actions workflow
   (Stage 28), asserting the process exits cleanly and prints the expected
   verdict. This is real end-to-end confirmation that a change hasn't
   silently broken something the unit tests don't cover (e.g. an argument-
   parsing regression, a build that compiles but panics on real input) —
   near-zero effort given the CI infrastructure and benchmark files already
   exist, and it closes a real gap (unit tests never actually invoke the
   built binary).

### Tier 2 — real value, bigger or more open-ended

6. **Periodic rephasing from a WalkSAT burst — my concrete answer to the
   "CDCL/local-search hybrid" question above.** Moderate effort (reuses
   existing `ws`/phase-saving code, needs a hook into `cdcl`'s restart
   loop and a policy for how often to trigger it), real literature backing
   (CaDiCaL, Kissat), directly addresses your stated interest without the
   much larger cost of a from-scratch hybrid architecture.
7. **Blocking-literal check, gated by clause length (`REPORT23.md`
   "bigger possible changes" item 1).** The most concretely testable of
   `REPORT23.md`'s three deferred items — a small, well-specified change
   (`if clause.len() > N && is_true(other_watch) { skip }`) whose payoff is
   real but narrow: helps on long/structured clauses (industrial instances,
   long learned clauses), does nothing or slightly hurts on this project's
   originally-dominant short random-3-SAT clauses. Now more actionable than
   when `REPORT23.md` wrote it, since Stage 29 substantially grew the
   structured/industrial corpus needed to pick a real threshold instead of
   guessing one.
8. **Chronological backtracking.** Explained above; a real, literature-
   backed CDCL refinement, genuinely bigger and more correctness-sensitive
   than most items in this tier. `REPORT22.md` deprioritized it pending
   "profiling shows backtracking cost actually matters" — Stage 23's
   profiling found `analyze`'s *allocation* cost matters enormously (item
   4 above), which is adjacent but not quite the same claim (that's
   learned-clause construction cost, not backtracking depth/frequency
   itself) — I don't think the trigger condition is fully met yet, but it's
   closer than it was.
9. **Auto-tuning harness + `.vibe_sat.json` config (`REPORT22.md` item
   12) — missing from your list.** Sequenced, per `REPORT22.md`'s own
   plan, after documenting parameters (item 15 below) and before deciding
   what's worth exposing. A genuine infrastructure investment (build a
   sweep harness, decide what's worth a config knob) that pays for itself
   across several other items on this list (Luby base, LRB alpha, a new
   `SelectVar` combination function, a blocking-literal threshold) rather
   than being valuable in isolation.
10. **Lookahead solver (fifth algorithm).** Discussed above. The single
    biggest implementation effort on this list — comparable to building
    `dfs`+BCP or `cdcl` from scratch, in both languages — with a real but
    narrow payoff (wins clearly on random/crafted instances, likely loses
    clearly on industrial ones). Worth doing if a "fundamentally different
    algorithm" is genuinely what you want next and you're comfortable with
    a multi-stage investment; not a quick win.

### Tier 3 — worth doing, modest effort or modest payoff

11. **UNSAT proof logging (DRAT) + independent checker (`REPORT22.md`
    item 9) — missing from your list, still fully unimplemented** (I
    checked: no DRAT support anywhere in either language, only a citation
    in `docs/references.md`). This is the one real gap in an otherwise
    very thorough verification culture — every SAT verdict gets
    independently re-checked from scratch, but UNSAT verdicts outside the
    labeled SATLIB sets are trusted on cross-language agreement alone.
    Higher effort than it might look (needs `cdcl` to emit a trace and
    something to check it against, e.g. `drat-trim`), and it's a
    trust/rigor investment rather than a solving-speed one — valuable
    given how much this project already invests in verification, but
    doesn't make anything faster.
12. **LRB's omitted refinements (the *real* item 16 your "LRB" section
    should have quoted): reason-side-rate bonus, annealed alpha
    (`REPORT13.md`).** Low-to-moderate effort (two specific, well-defined
    pieces of an already-partially-implemented paper), uncertain payoff —
    `REPORT13.md`'s "LRB underperforms VSIDS here" finding was explicitly
    provisional pending exactly these, but VSIDS already works fine as the
    default, so this is mostly about intellectual completeness rather than
    an expected win. Worth it only if you want a definitive answer on LRB
    specifically, as `REPORT22.md` already said.
13. **Progress meter / heartbeat (`REPORT22.md` item 11) — missing from
    your list.** Cheap (a periodic, time-gated print at high `--verbose`,
    the design `REPORT22.md` already sketched and you never responded to
    either way). Low effort, low-but-real payoff (UX only, no solving
    change) — I'd batch this in alongside anything else touching the
    search loops rather than as its own stage.
14. **Adaptive Novelty+.** Already agreed low priority (`hc`/`ws` aren't
    the project's current focus); "eventually" is the right framing and I
    have nothing to add.
15. **List every internal parameter in one place (`REPORT22.md` item 7,
    first half).** Low effort (a reference doc, no code), and a real
    prerequisite for item 9's harness above — worth doing whenever you
    pick up tuning anything, not urgent standalone.
16. **Luby restart base recalibration.** Low effort once item 9's harness
    exists; not worth doing ad hoc before that, per `REPORT22.md`'s own
    plan.

### Tier 4 — low priority, real but small, or blocked on a decision only you can make

17. **Expand the structured/industrial benchmark corpus, your "New:
    Benchmarks" question.** Stage 29 already added 400 real SAT
    Competition 2018 files (~15 GB, local-only) specifically because
    `REPORT21.md` noted clause-sharing's benefit is more visible on
    industrial instances — that's a substantial corpus already in place.
    More recent SAT Competition years (2019-2024) would mostly add more of
    the same *kind* of signal rather than a new kind; I'd only prioritize
    this if a specific upcoming item (blocking-literal threshold tuning,
    item 7 above) needs more data than what's already local.
18. **Import recent SAT conference papers, your "New" question.** I think
    this is worth doing eventually, but as its own scoped stage rather
    than folded into this one — "read N papers and report back" is
    genuinely open-ended effort with unpredictable output, unlike
    everything else on this list, which I could size and rank because I
    already know roughly what each item involves. If you want it, I'd
    frame it narrowly (e.g. "survey the last 3 years of SAT Competition
    application-track winners' description papers for techniques not on
    this list") rather than an unbounded literature dump.
19. **Try Option D for CDCL (`REPORT22.md` item 5).** Still just as
    speculative as when `REPORT22.md` deprioritized it — I haven't seen
    Option B's portfolio design show a real weakness in any stage since,
    including Stage 32's own dfs-focused work. I'd leave this alone until
    something concrete surfaces a reason to revisit it.
20. **Thread BVE (`REPORT25.md`'s open question) — missing from your
    list, and now lower priority than when originally raised.** This is
    distinct from Stage 31's BVE fix: Stage 31 fixed BVE's *algorithmic*
    cost (the `O(vars×clauses)` rebuild-per-elimination bug); this item is
    about *parallelizing* the (now much cheaper) technique across threads,
    which `REPORT25.md` found awkward (many small, per-round thread-spawn
    batches don't parallelize cleanly without a persistent worker pool).
    Given Stage 31 already delivered BVE's big win, I'd downgrade this
    from where `REPORT25.md` left it.
21. **A standing Go-vs-Rust benchmark harness (`REPORT22.md` item 18).**
    Effectively superseded by Stage 24's `util/benchcompare`, which is
    exactly this, in Go only (a deliberate choice per `REPORT24.md`). I'd
    call this done in spirit; extending it to also drive comparisons from
    Rust is a small, optional follow-up, not a real gap.
22. **Per-thread `--verbose` breakdown (`REPORT22.md` item 19) — missing
    from your list.** Small, cosmetic, data already collected — do it
    whenever convenient, not worth its own stage.
23. **A genuinely different watch-list representation (`REPORT23.md`
    "bigger possible changes" item 3).** `REPORT23.md`'s own conclusion —
    "there's no single wasteful thing left to trim... real research-grade
    solver engineering" — still stands; I'd rank this lowest bang-for-buck
    on the whole list, high effort for uncertain-to-marginal payoff.
24. **Full benchmark sweep to exhaustion (`REPORT22.md` item 22).**
    Unchanged from `REPORT22.md`'s own assessment: background confidence-
    building, not a real priority. Every sampled sweep across 32 stages
    has been clean.
25. **`bveWorkBudgetFactor` tuning / length-based elimination heuristic
    for pathological files (`REPORT31.md`).** Narrow — affects one known
    file (`apn-sbox5-cut3-symmbreak.cnf`) — `REPORT31.md` already said
    this doesn't block anything currently in scope. Lowest priority on
    this list.

### Already resolved since `REPORT22.md` — dropping from the active list

Items 1 (profile CDCL, Stage 23), 2 (parallelize preprocessing, Stages 25/
30/31), 3 (`dfs` deque lock-free rewrite, Stage 26), 4 (clause-database
contention, closed with a negative answer in Stage 23), 6 (README
sub-pages, Stage 27), 8 (license — a `LICENSE` file now exists in the repo
root, I did not check when or how it was added), 10 (CI, Stage 28), and 20/
`REPORT22.md` item 20 (`dfs`'s non-monotonic UNSAT scaling — resolved,
confirmed monotonic in Stage 32's own remeasurement) are all done. I did
not re-litigate any of these.

## Summary of my recommendation

If I were sequencing the next few stages myself: **LBD-based clause
management (Tier 1, item 1)** first — it's the highest-value item on this
entire list and nothing currently in the codebase does anything like it.
**The time-based time-check interval (item 2)** right alongside or
immediately after it, since it's a confirmed reliability bug with a
contained fix, not a guess. **Learned clause minimization (item 3)** pairs
naturally with both (touches the same `analyze` code path as LBD, and
directly shrinks the item-4 allocation problem before anyone builds a
bigger fix for it). **The smoke test (item 5)** is cheap enough to just do
whenever, independent of everything else. Past that, it genuinely depends
on what interests you more: the periodic-rephasing hybrid (item 6, a
concrete, moderate-effort answer to your CDCL-hybrid question), or the
lookahead solver (item 10, the biggest single bet on this list but a real
new algorithm and a real new Go-vs-Rust data point).

## Questions for you

- Does LBD-based clause management + the time-check fix + clause
  minimization, roughly in that order, sound like the right next few
  stages, or would you rather prioritize the CDCL/local-search hybrid or
  the lookahead solver first?
- For `SelectVar`'s literal-combination function: want me to just
  implement 2-3 candidate `F`s (sum, product, min/max) behind a flag and
  benchmark them against each other and the current default, rather than
  picking one from theory alone?
- Any interest in the DRAT/independent-UNSAT-checker item, given how much
  this project already invests in verification elsewhere? It's real work
  with no speed payoff, so I want your read on whether it's worth it to
  you specifically, not just "technically a gap."
- Want me to scope "import recent SAT conference papers" as its own
  narrow future stage (e.g. "survey the last 3 years of application-track
  winners' papers"), or drop it for now?
