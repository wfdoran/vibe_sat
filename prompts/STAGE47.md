# Stage 47

With the recent improvements, let's repeat the performance survey done
in stage 40.

Use the same set of problems and the same timeout.  Also we only
testing the CDCL versions.

If as a result of this survey, you want to push any tasks to the
top of the list let me know.

## REPORT46.md Questions

- `REPORT45.md`'s other open question — a broader benchmark sweep
  against `minisat`/`cryptominisat5` re-running something like
  `REPORT40.md`'s own comparison — is still open (you deferred it in
  favor of this `dfs` fix). Worth doing now that both `cdcl` and `dfs`
  have the fix, or still lower priority than whatever's next on
  `low_priority.md`?

I think this is good survey for now.  It gives us some idea of how we
are doing.

- Given how much smaller `dfs`'s payoff was, and that `SelectVar`
  (not BCP) is `dfs`'s own actual bottleneck on its default
  configuration — is a future look at `SelectVar`'s own cost (it
  rescans every not-yet-satisfied clause on every node, by design)
  worth scoping, or is `dfs` performance simply not a current
  priority the way closing the `minisat` gap on `cdcl` was?

I will add this to my todo list.  My focus is on CDCL though.

