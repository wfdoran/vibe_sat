# Stage 55

Let's work on

1. **Clause import cursor is never re-synchronized (by reading, not
   measured).** `maybeImport` (`clauseshare.go`) advances each peer cursor by
   exactly 1 per check (every 32 conflicts) and never clamps it to the peer's
   `writeIdx`. Two consequences: the import rate is at most one clause per
   peer per 32 conflicts, far below what peers publish, so sharing is much
   weaker than the design intends; and once a slow-exporting peer's ring has
   wrapped, the cursor can run ahead of `writeIdx` and re-read slots already
   imported, importing duplicates. Neither is a correctness problem. A cheap
   fix is to drain up to N clauses per check and clamp the cursor to
   `writeIdx`. It needs a benchmark to see whether more sharing actually
   helps; I have not run one.


## Questions from REPORT54.md

1. **`-p _` alone for `hc`/`ws`:** keep STAGE44's "use the default"
   semantics (current behavior), or require at least one real value?

For both hc and ws, the user must either tell us how many iterations
to perform or give a time_limit.  If neither is given, that should be
an error.

2. **`main_test.go` and `.gitignore`:** OK to fix the ignore pattern (I may
   not edit it), or will you `git add -f` the test file?

I fixed this.

3. **Validate the other params (Phase 3, item 2)?** Only the crashing one is
   validated today.

We will have a stage on this. 

4. **Time-limit guard for preprocessing:** worth a dedicated stage now that
   the biggest known overrun is fixed?

I added this to todo.md.  

5. **Clause-import cursor (Phase 3, item 1):** worth a small benchmarked
   stage?

This stage!