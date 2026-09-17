# Stage 30

From REPORT29.md

2. **Preprocessing's O(clauses²) subsumption elimination is a severe,
   widespread problem on this benchmark class — not just on the
   handful of largest files.** A sample of small files (≤3 MB!) mostly
   blew a 45-second hard timeout with preprocessing on, while the
   identical files (plus larger ones, up to 5 MB) all comfortably
   finished within a 15-second search budget with `--no-preprocessing`.
   This is a much more urgent, much more broadly-triggered version of
   the same finding `REPORT25.md` made on `benchmark/blocksworld`'s
   two largest files alone.


Let's fix this.

Is there a O(clauses log(clauses)) algorithm for subsumption elimination?
Seems like there should be one.

If yes, implement it.

If not, have some bound on clauses above which you don't run
subsumption

