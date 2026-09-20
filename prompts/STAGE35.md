# Stage 35

In this stage, let's complete two tasks: tune glucose param and
update the time-check interval.  


# Tune Glucose parameter

From REPORT34.md

- Given the negative benchmark result above, any interest in a
  follow-up that tunes Glucose's own parameters (`glucoseK`,
  `glucoseWindowSize`) against this project's benchmark set the way
  `REPORT15.md` tuned the Luby base — or is `val2=5` fine to leave as
  a correctly-implemented, non-default option and move on to the next
  stage?


Yes, please optimize this parameter.  Also when do the optimize
all parameters stage, be sure this one is on the list.


# Improved time-check

From REPORT33.md

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