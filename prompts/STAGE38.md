# Stage 38

Let's look at this item next.

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


We have done item 3.  So a survey might be in order to decide if
this is still necessary.  

Go does have memory pools which might be helpful, not sure.

