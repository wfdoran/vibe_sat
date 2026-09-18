# Stage 34

## Next Task

Let's do your top item on REPORT33.md: LBD-based clause management.

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

## Questions

- Does LBD-based clause management + the time-check fix + clause
  minimization, roughly in that order, sound like the right next few
  stages, or would you rather prioritize the CDCL/local-search hybrid or
  the lookahead solver first?

I would rank them

1. clause management + the time-check fix + clause
  minimization.  These will probably be our next three stages

2. CDCL/local-search hybrid.  Intrigued.

3.  lookahead solver.  Leary.  Given that it known worse on some
  benchmarks.  I want to put this on a back burner. 

- For `SelectVar`'s literal-combination function: want me to just
  implement 2-3 candidate `F`s (sum, product, min/max) behind a flag and
  benchmark them against each other and the current default, rather than
  picking one from theory alone?

Yes.  We should choose based on benchmark tests.  This will likely
come after clause management + the time-check fix + clause min.  

- Any interest in the DRAT/independent-UNSAT-checker item, given how much
  this project already invests in verification elsewhere? It's real work
  with no speed payoff, so I want your read on whether it's worth it to
  you specifically, not just "technically a gap."

I personally am not interested in this.  I understand that for
competitions this is now needed for UNSAT problems.  

- Want me to scope "import recent SAT conference papers" as its own
  narrow future stage (e.g. "survey the last 3 years of application-track
  winners' papers"), or drop it for now?

That would be great.  I will ask you to do this in few stages.  


## Documentation

Have we been keeping `docs/references.md` up to date when we add
new features?  If not, please update and make this a standard part
of each stage.

