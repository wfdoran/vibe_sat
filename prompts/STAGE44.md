# Stage 44

I want to do a quick side stage on some parameter changes.

1. For --alg-params (-p), allow the use an underscore '_' to indicate
   "use the default value".

2. For --algorith=cdcl, for --alg-params val2 lets swap 4 and 5

                 4 = round-robin (STAGE21.md, --num-threads > 1 only):
                     worker 0 uses quadratic, worker 1 geometric,
                     worker 2 Luby, worker 3 quadratic again, and so
                     on, so each strategy runs on close to an equal
                     share of the workers instead of every worker
                     racing with the same restart cadence.
                 5 = Glucose's own data-driven policy (STAGE34.md):
                     restarts not on a fixed conflict-count schedule
                     but whenever the moving average LBD ("Literal
                     Block Distance", Audemard & Simon 2009) of the
                     last 50 learned clauses is close to or worse than
                     the all-time average LBD -- a sign the search has
                     drifted into learning less useful clauses than
                     its own history and is better off restarting.

  Further value 5 (round-robin) includes the glucose and round-robins
  among the four choices: quadratic, geometric, luby, and glucose.


## REPORT43.md questions

- Worth a `util/paramtune` sweep over `rephaseIntervalRestarts`/
  `rephaseMaxFlips` specifically (mirroring Stage 42's
  `bveWorkBudgetFactor` follow-up), to check whether WalkSAT rephasing
  can be made to actually help at a different interval/budget, before
  writing it off entirely? It was a near-tie at these first-guess
  defaults, not a clear loss the way target phase was.

Keep this in mind for the next time I want to do a brainstorming stage.

- Given target phase's simplified (no mode-cycling) implementation
  measured clearly worse standalone, is it worth a future stage
  building the fuller Kissat-style rotation this literature actually
  describes (cycling between saved/target/inverted/original/random
  phases across stabilizing-mode restarts), or does that cross into
  "bigger algorithmic change" territory you'd rather scope separately
  (`REPORT41.md`'s tier 3), given this stage's own measurement didn't
  find the simplified version worth keeping on its own?

Keep this in mind for the next time I want to do a brainstorming stage.

- The multi-threaded `PhaseRoundRobin` default carries over restart's
  diversification precedent by analogy, not independent multi-threaded
  measurement — worth a dedicated benchmark of multi-threaded `cdcl`
  with vs. without phase diversification before trusting it, or is the
  architectural argument (diversity helps the shared clause pool)
  convincing enough on its own?

We will sort of fix this with stage.  The SelectVar heuristic will be
in a 4 cycle and the Phase heuristic will be on a 3 cycle.  gcd(4,3) =
1.  But we might want to switch from round-robin to random at some
point.
