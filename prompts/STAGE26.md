# Stage 26

Let's fix **Go's `dfs` deque: lock-free rewrite.**

## REPORT22 Notes

   clause resolution scales badly), but it's a guess.
3. **Go's `dfs` deque: lock-free rewrite.** Not on your list, but it's
   the strongest *already-confirmed* performance finding sitting
   unaddressed anywhere in the project: two independent benchmarks
   (`REPORT18.md`'s `uf250` SAT numbers, `REPORT19.md`'s `uuf175`
   UNSAT numbers) show Go's hand-rolled mutex-guarded deque plateauing
   past 4 threads while Rust's lock-free `crossbeam-deque` keeps
   scaling to 16. I flagged it in `REPORT19.md`'s open questions and
   never returned to it. A real design change (an atomic-pointer-swap
   or Chase-Lev-proper lock-free deque in Go), not a quick fix.

