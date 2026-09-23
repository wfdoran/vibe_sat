# Stage 46

From REPORT45.md, let's do this next.

- `dfs` has the identical inefficiency this stage just fixed in
  `cdcl` (see above) — worth its own stage applying the same fix, or
  is `dfs`'s own performance not currently a priority the way closing
  the `minisat` gap was?


## REPORT45.md Questions

- This stage's fix was purely internal (no CLI surface, no new
  parameter) — is a broader benchmark sweep against `minisat`/
  `cryptominisat5` (re-running something like `REPORT40.md`'s own
  comparison) worth doing now, to see how much of that original gap
  this closes, or is the direct old-vs-new measurement in this report
  convincing enough on its own for now?

Yes, I planning on doing this next until I saw the DFS suggestion. 


- `REPORT23.md`'s doc comment on `chooseWatch` still describes a
  rejected "check if the other watch is already true before scanning"
  optimization that measured worse *given the old candidate-discovery
  cost dominating everything else*. Worth re-measuring that
  specifically now that propagate's own overhead has dropped
  substantially, in case the balance of costs has shifted enough to
  change that earlier conclusion?

I will add this to my todo.md list.

