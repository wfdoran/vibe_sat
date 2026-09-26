# Stage 51

Let's work on chooseWatch next.

1. **`chooseWatch` remembering a scan position across calls
   (`REPORT41.md`/`REPORT47.md`).** The best-value item on this list:
   a real, standard, well-understood technique, squarely aimed at the
   function every recent profile has named as the dominant cost, and a
   natural continuation of the Stage 45→48 story rather than a new
   direction.


## REPORT50.md Questions

- Tier 1's three items are all cheap-to-moderate and each closes a
  real, specific gap rather than chasing a diffuse improvement — does
  that ordering (watch-scan position, then bootstrap interruptibility,
  then the progress meter) match your own sense of priority, or would
  you reorder?

I am happy with the order

- Of the two new preprocessing ideas (BVA, clause vivification), both
  are real literature techniques this project has no analogue of yet
  — worth prototyping both eventually, or do you want me to pick one
  to scope first?

Eventually, we will do both.  I am not sure the exact order matters.

- The incremental-solving/assumptions API is the biggest idea on this
  list by far and touches the CLI surface in both languages — is that
  a direction you're actually interested in, or was it worth
  mentioning once and then leaving alone unless something changes?


I can see a use case for this.  Maybe we run for while with one
variable selection routine.  Then swtich to another.  I could also
see this as a way to do checkpointing on a long run.  