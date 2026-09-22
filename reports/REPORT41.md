# Report: Stage 41

## Summary

`STAGE41.md` asked for a no-code brainstorm: given `REPORT40.md`'s
measured gap against `minisat`/`cryptominisat5`, what would close it,
sorted into code improvements, tweaks, and a big algorithmic change —
then ranked. No code changes this stage, one small piece of
zero-new-code measurement (re-running an *existing* benchmark with
`-cpuprofile`, no source edits) to ground the ranking in fresh
evidence rather than only citing old reports.

**Ranked recommendation, short version:**

1. **Auto-tune internal parameters against the 250-variable random set
   specifically** (tweak). You already flagged this as your top pick
   answering `REPORT40.md`'s question, and I agree: `util/paramtune`
   already exists, the benchmark corpus (`benchmark/uf250-1065`,
   `benchmark/uuf250-1065`, 100 files each) already exists, and this
   is exactly the instance class where `REPORT40.md` measured the
   clearest same-family gap. Cheapest real lever on this whole list.
2. **Try phase-selection techniques `vibe_sat` doesn't have yet**
   (tweak) — specifically *target phases* (Chanseok Oh; standard in
   CaDiCaL/Kissat since ~2015), and/or `REPORT33.md`'s already-scoped
   *periodic rephasing from a WalkSAT burst*. Cheap-to-moderate, real
   literature backing, untried here, plausible win on both random and
   some industrial instances.
3. **A genuinely different watch-list representation** (code
   improvement). `REPORT23.md`/`REPORT38.md` already profiled this
   exact instance family (`uuf250-1065`) at 8 and 32 threads and found
   `propagate`/`chooseWatch` — the watched-literal BCP scan — at
   95-96% of CPU time; a fresh single-threaded re-profile this stage
   (see below) found it even more concentrated, **97.66%**. This is
   the single highest-ceiling lever for exactly the gap `REPORT40.md`
   measured on random instances, it's the item you already told me
   you'd "bumped up" your own list after `REPORT38.md`, and now
   there's an external, concrete number attached to the cost of not
   doing it. High effort, real risk, but no longer a guess about
   whether it matters.
4. **XOR/Gaussian elimination detection** (big algorithmic change).
   This is the most literature-precise explanation for
   `cryptominisat5`'s two *unique* wins in `REPORT40.md`
   (`ortholatin-7`, `prime_119218851371`) — it's `cryptominisat5`'s
   own signature technique, not a generic "better solver" effect, and
   it plausibly extends to the two multiplication-circuit instances
   both external solvers also struggled with (`factoring...`,
   `Karatsuba...`). High effort, genuinely new machinery, but the most
   surgically targeted response to the industrial half of
   `REPORT40.md`'s findings, rather than a diffuse "engineer harder"
   bet.
5. **Blocking literals, gated by clause length this time**
   (tweak/code-improvement hybrid, `REPORT33.md` item 7). Already
   tried once *unconditionally* (Stage 23) and reverted after a
   measured 25-28% regression on short clauses — the length-gated
   version was never actually built. Moderate effort, narrower payoff
   than items 3-4, but concretely aimed at exactly the "long
   clauses/industrial" side of the gap.
6. Everything else already on `REPORT33.md`'s backlog (chronological
   backtracking, periodic inprocessing beyond rephasing, LRB
   refinements, a lookahead solver) — real, but not clearly connected
   to *this specific* measured gap; ranked below the five above for
   that reason, not dropped.

## Where the gap actually is (recap, `REPORT40.md`)

Two distinct gaps, not one, and they call for different fixes:

- **Random 3-SAT at 250 variables**: `minisat`/`cryptominisat5` ran
  2.3-9x faster than `vibe_sat` (Go and Rust) on both the SAT and
  UNSAT instance. Same family as every smaller random instance tested,
  where there was *no* meaningful gap — this is a scaling/throughput
  story, not a missing-technique story.
- **Industrial instances**: `minisat` and `cryptominisat5` together
  solved one instance (`huck.col.11`) neither `vibe_sat` could within
  60s; `cryptominisat5` *alone* solved two more (`ortholatin-7`,
  `prime_119218851371`) that even `minisat` couldn't. Six of nine
  industrial instances timed out for all four solvers, `minisat` and
  `cryptominisat5` included — most of the industrial set is just hard
  for everyone at this budget, but the instances where a real gap
  showed up point at specific missing capabilities, not raw speed.

## Class 1: Code improvements (profile, find hot spots, fix them)

**For the random-instance gap, this is already mostly done** —
re-profiling wasn't guesswork this stage, it was confirming whether an
old finding still holds. `internal/cdcl/bench_test.go`'s existing
`BenchmarkRunHardSingleThreaded` (unchanged since Stage 23, no new
code needed) against `benchmark/uuf250-1065/uuf250-01.cnf` — the same
family and size as `REPORT40.md`'s `uuf250-0100.cnf` gap — re-run with
`-cpuprofile` this stage:

```
      flat  flat%   sum%        cum   cum%
     9.49s 52.96% 52.96%     17.50s 97.66%  (*solver).propagate
     5.30s 29.58% 82.53%      7.93s 44.25%  chooseWatch (inline)
     1.55s  8.65% 91.18%      1.55s  8.65%  cnf.Literal.Var (inline)
     1.18s  6.58% 97.77%      2.69s 15.01%  isFalse (inline)
```

`propagate`/`chooseWatch` — the watched-literals unit-propagation
scan — is **97.66%** of single-threaded CPU time, even more
concentrated than `REPORT23.md`'s original 8-thread finding (95%) or
`REPORT38.md`'s 32-thread re-check (95-96%). Three independent
profiling passes, three different thread counts, same answer: there
is exactly one hot function family, and it hasn't moved since Stage
23. **There is nothing left to discover by profiling this instance
class further** — the actionable question is entirely "is a different
watch-list representation worth building," which is Class 1's other
item, discussed under the ranking above and in "Big algorithmic
change" for effort comparison.

**For the industrial-instance gap, no equivalent profile exists yet.**
Every existing `cdcl` profile (Stage 23, this stage) targets random
3-SAT; the one industrial profile in the project's history
(`REPORT24.md`, `bw_large.c.cnf`) measured *preprocessing*, not
`cdcl`'s search loop, and that bottleneck was fixed by Stages 25/31.
Structured/industrial clauses have a different shape (longer, more
skewed occurrence patterns) than random 3-SAT's uniform short clauses,
so `propagate`/`chooseWatch` dominating there too is a reasonable
guess, not a confirmed fact. **A cheap, concrete next step**: profile
`cdcl` on one of the industrial instances that actually finishes
(`huck.col.11`, ~18-35s for the external solvers, so within reach of a
`-benchtime=1x` profile) to check whether the same hot spot holds
before assuming a random-instance fix (the watch-list item) would
transfer to industrial instances too.

## Class 2: Tweaks

- **Auto-tune internal parameters against the 250-variable random
  set** (ranked #1 overall — see Summary). Concretely actionable
  today: `util/paramtune --dirs=uf250-1065,uuf250-1065 --sample=20
  --algorithm=cdcl --time-limit-secs=60 --sweep="cdcl.<param>=..."`
  against the restart-schedule bases, `glucoseK`, and the decay rates
  — the exact parameters `docs/internal-parameters.md` already
  documents as tuning candidates, now with a specific, externally-
  motivated target (this instance class) rather than tuning in the
  abstract.
- **Phase-selection techniques not yet tried** (ranked #2 overall):
  - *Target phases* (Chanseok Oh): instead of pure phase-saving,
    periodically recompute a "best known" phase per variable from a
    cheap local signal and bias decisions toward it. Standard in
    CaDiCaL/Kissat; `vibe_sat` has phase-saving (Stage 14) but nothing
    like this.
  - *Periodic rephasing from a WalkSAT burst* — `REPORT33.md` item 6,
    already scoped in detail (reuses `ws`'s existing loop and `cdcl`'s
    `savedPhase` array), never implemented.
  Both are cheap relative to their literature backing and untested
  here; either is a reasonable moderate-effort bet independent of the
  parameter sweep above.
- **Blocking literals, gated by clause length** (`REPORT33.md` item
  7, ranked #5 overall) — see Summary. The one tweak aimed
  specifically at the industrial/long-clause side of the gap rather
  than the random-instance side.
- **LRB's omitted refinements** (`REPORT33.md` item 12) — lower
  priority for *this* purpose specifically: `REPORT40.md`'s gap shows
  up as a raw-throughput/technique gap, not a "wrong branching
  heuristic" gap (VSIDS already measurably beats LRB here per
  `REPORT13.md`), so this doesn't move the needle on what was actually
  measured.

## Class 3: A big algorithmic change

- **XOR/Gaussian elimination detection** (ranked #4 overall — see
  Summary). `cryptominisat5`'s own defining technique (Soos, Nohl &
  Castelluccia, "Extending SAT Solvers to Cryptographic Problems," SAT
  2009): detect chains of clauses that jointly encode an XOR/parity
  constraint (common in cryptographic and arithmetic circuit
  encodings — multiplication, factoring, checksum/parity structures),
  extract them, and reason over the extracted system with Gaussian
  elimination over GF(2) alongside ordinary CDCL. This is a real,
  substantial new subsystem (XOR extraction from raw CNF, an in-solver
  linear-algebra reasoning engine, keeping it consistent with CDCL's
  trail/backtracking), but it's the only item on this whole list
  precisely targeted at *why* `cryptominisat5` specifically — not
  `minisat`, which has no such capability and also failed on those two
  files — pulled ahead on exactly two instances in `REPORT40.md`.
- **Periodic/heavier inprocessing** (re-running simplification
  techniques *during* search, not just once before it) — a real,
  general modern-solver technique, but more diffuse than XOR
  extraction: it's "simplify more, more often" rather than "add a
  specific missing capability," so it's harder to predict which of the
  6 still-unsolved industrial instances it would actually help, if
  any. Ranked below the XOR item for that reason.
- **Chronological backtracking** (`REPORT33.md` item 8) — real,
  literature-backed, but not connected to any specific finding in
  `REPORT40.md`; still open, still correctness-sensitive, still
  reasonable to defer per `REPORT33.md`'s own framing.
- **A lookahead solver** (`REPORT33.md` item 10) — explicitly the
  *wrong* tool for the industrial half of this gap: `REPORT33.md`'s
  own research found lookahead solvers reliably lose on industrial/
  application instances (which is most of where `REPORT40.md`'s gap
  actually is) and only reliably win on random/crafted instances. It
  remains a reasonable "build a fifth algorithm for its own sake" bet,
  just not a good answer to *this* stage's specific question.

## Verification

No code changes this stage. The one new measurement (the
single-threaded `cdcl` CPU profile above) used
`internal/cdcl/bench_test.go`'s existing `BenchmarkRunHardSingleThreaded`
unmodified, via `go test -bench=... -cpuprofile=...` — no source
files touched. The resulting `.prof` file and the stray `cdcl.test`
binary `go test -cpuprofile` leaves behind were both deleted before
finishing, per the standing cleanup rule.

## Documentation

No updates — a planning/ranking stage doesn't change any shipped
behavior, CLI surface, or algorithm.

## Questions for you

- Given the ranking above, should Stage 42 start with the parameter
  sweep (#1, cheapest, most concrete) and the phase-selection tweaks
  (#2), and treat the watch-list rewrite (#3) and XOR elimination (#4)
  as their own larger, separately-scoped future stages once the
  cheaper items are done? That's the sequencing I'd default to absent
  other direction.
- The watch-list representation item now has two independent signals
  pointing at it (your own "bumped up my list" note after
  `REPORT38.md`, and `REPORT40.md`'s concrete numbers) — worth
  scoping as its own dedicated stage soon, or still fine to sequence
  behind the cheaper tweaks first?
- XOR/Gaussian elimination is a genuinely large subsystem — worth a
  small preliminary spike (confirm how many industrial instances in
  the local `sat_comp/2018` corpus actually contain extractable XOR
  structure, before committing to building the extraction+solving
  machinery) rather than committing to the full feature outright?
