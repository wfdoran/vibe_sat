# Stage 39

## REPORT38.md

I agree with your assessment that no action is needed here.

- Any interest in re-running this same survey against a much larger
  real instance (e.g. from `sat_comp/2018`) to see whether the
  conclusion changes at a different scale, or is this project's own
  standard benchmark instance representative enough to trust as-is?

No.  I think we are good.

- `REPORT23.md`'s item 3 (a genuinely different watch-list
  representation) is now the only concrete lever left for `cdcl`'s
  core-loop performance. Worth scoping as its own future stage, or
  staying on the back burner per the project's existing "real
  research-grade solver engineering" framing?

I bumped this up on my todo list

## This Stage

Let's go after three related items:

15. **List every internal parameter in one place (`REPORT22.md` item 7,
    first half).** Low effort (a reference doc, no code), and a real
    prerequisite for item 9's harness above — worth doing whenever you
    pick up tuning anything, not urgent standalone.


9. **Auto-tuning harness + `.vibe_sat.json` config (`REPORT22.md` item
   12) — missing from your list.** Sequenced, per `REPORT22.md`'s own
   plan, after documenting parameters (item 15 below) and before deciding
   what's worth exposing. A genuine infrastructure investment (build a
   sweep harness, decide what's worth a config knob) that pays for itself
   across several other items on this list (Luby base, LRB alpha, a new
   `SelectVar` combination function, a blocking-literal threshold) rather
   than being valuable in isolation.


25. **`bveWorkBudgetFactor` tuning / length-based elimination heuristic
    for pathological files (`REPORT31.md`).** Narrow — affects one known
    file (`apn-sbox5-cut3-symmbreak.cnf`) — `REPORT31.md` already said
    this doesn't block anything currently in scope. Lowest priority on
    this list.


Comments:

- Design question: should the ability of the user to set the internal
  parameters be implicit (if .vibe_sat.json exists, use it) or explicit
  (new command line parameter --internal-params=<filename>)?

- We will want some way to write out the default internal parameters
  --reset-internal-param (-q) causes vibe_sat to write out
  .vibe_sat.json with its defaults.  The user can then edit this if they
  want.

- I am hoping that item 25 will follow for free form 9 and 15.
  bveWorkBudgetFactor is one of the internal params which we optimize
  over.

- Are you going to optimize the parameters one by one or try to
  optimize multiple param at the same time?

