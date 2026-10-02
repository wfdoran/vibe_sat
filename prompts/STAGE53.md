# Stage 53

Let's do the progress meter next.

3. **Progress meter / heartbeat (`REPORT13.md`/`REPORT22.md`).** Cheap,
   real UX payoff, has been sitting unaddressed the longest of
   anything on this list (`REPORT22.md` first sketched it; you never
   responded either way).

With --verbose >= 1 --algorithm DFS or CDCL, every t^2 seconds (1, 4,
9, 16, ...), print out one line with some info on the progress.  I
don't know exactly what to print out.  You might want to look at
minisat's output for some ideas.  Whatever you do print out, computing
the value being displayed should not be computationally demanding.

For --algorithm HC and WS, I don't know what you can print out.  maybe
just which iteration number you are on and the highest scoring
solution found so far.

## REPORT52.md questions

- The disclosed `preprocess.Run` gap (Stage 8's own pipeline running
  entirely outside `--time-limit-secs`) is arguably bigger than what
  this stage fixed, but also a bigger lift (subsumption/BVE would each
  need their own deadline threading). Worth its own future stage, or
  is "the search's own defensive re-propagation is now interruptible"
  enough for now, matching how `REPORT35.md` itself left this exact
  question open before?

I put it on my todo list.


- Go's and Rust's "before" baselines for the big-file measurement
  weren't perfectly matched (see the methodological note above) — I
  judged this didn't matter given the scenario is bootstrap-dominated,
  not search-loop-dominated, but flagging the reasoning rather than
  silently presenting the numbers as a clean controlled pair.

This is ok.

- Per `REPORT50.md`'s Tier 1 ordering, the progress meter /
  heartbeat is next. Still the right call, or does anything here change
  your sense of priority?

Yup, that is next.

