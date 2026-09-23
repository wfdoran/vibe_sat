# Stage 45

Let's go after this one next. 

3. **A genuinely different watch-list representation** (code
   improvement). `REPORT23.md`/`REPORT38.md` already profiled this
   exact instance family (`uuf250-1065`) at 8 and 32 threads and found
   `propagate`/`chooseWatch` — the watched-literal BCP scan — at
   95-96% of CPU time; a fresh single-threaded re-profile this stage
   (see below) found it even more concentrated, **97.66%**. This is
   the single highest-ceiling lever for exactly the gap `REPORT40.md`
   measured on random instances, it's the item you already told me
   you'd "bumped up" your own list after `REPORT38.md`, and now
   there's an external, concrete number attached to the cost of not
   doing it. High effort, real risk, but no longer a guess about
   whether it matters.

## low_priority.md

This is a list of suggestions that I view as low priority.  When
brainstorming ideas, there is no need to bring these up again.
Like todo.md, you are not to alter low_priority.md.


## Questions from REPORT44.md

- The restart/phase round-robin coprime-periods trick works well for
  exactly two rotating dimensions. If a third portfolio dimension
  (e.g. `SelectVar`, which already has exactly 4 possible values) ever
  gets its own round-robin option, worth planning for three mutually
  coprime periods from the start, or cross that bridge if/when it
  actually comes up?

Wait for it to come up.  We will probably want to do something
random.


- You mentioned switching from round-robin to random assignment "at
  some point" — is that still just a note for later, or is there a
  specific reason (e.g. round-robin's determinism making some worker
  index systematically luckier or unluckier across many runs) you'd
  want investigated first?

It is to avoid the issue you raised in the previous question.

