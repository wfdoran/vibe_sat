# Stage 52

Let's keep going down the list.  Bootstrap interruptibility next.

2. **Bootstrap interruptibility (`REPORT35.md`).** Closes the one gap
   the time-check fix left open — a large/adversarial instance can
   still make `--time-limit-secs` a lie during bootstrap specifically.
   Contained (the bootstrap functions are already isolated), directly
   fixes user-visible reliability.

## REPORT51.md Questions

- The per-file variance (most files faster, a few meaningfully slower)
  is inherent to changing search order, not a bug — comfortable with
  that tradeoff given the strong aggregate result, or worth me digging
  into `uf250-03.cnf` specifically to understand why it regressed so
  much?

Not now, but lets add this file to minisat, cryptosat comparision
list if it not already one one of the file we use 

- `REPORT50.md`'s Tier 1 also listed bootstrap interruptibility and the
  progress meter, in that order — want me to continue straight into
  bootstrap interruptibility next, or is there anything about this
  stage's results you'd like to revisit first?

Yup that is next.



