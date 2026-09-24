# Stage 48

Let's do the CPU profile next.

Given your stated focus is `cdcl` specifically: **a fresh CPU profile
on the current, post-Stage-45/46 code** is the most informationally
valuable next step before picking a specific optimization target,
since the last profile (`REPORT41.md`, pre-fix) is now stale —
`propagate` no longer dominates at 97%+; `chooseWatch`'s own linear
scan for a replacement watch was already 52.91% of *post-fix* time in
the last profile taken right after Stage 45's change (see
`REPORT45.md`), and that's the more actionable candidate now, not
`propagate`'s own candidate-discovery cost, which this stage's fix
already addressed. I'd want a current number before recommending
anything more specific than "look at `chooseWatch` next" — this is
exactly the kind of claim this project's own culture says to measure
rather than assume.


If the profiling turns up an obvious fix, make it.  Otherwise,
make a list of the bigger changes which we might undertake.

