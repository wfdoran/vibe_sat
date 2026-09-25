# Stage 50

Time for another brainstorming stage.  Go through the various tasks
discussed previously but not acted on.  Also, include any that new one
that seem reasonable at this point.  Put them in priority order.

Please look at low_priority.md.  Any task I have put in that file does
not need to be listed in this stage.


## REPORT49.md Questions


- Item 12: LRB improved measurably on the SAT half and barely at all
  on the UNSAT half — does that settle "LRB just doesn't suit this
  benchmark set" for you, or is it worth a dedicated look at *why*
  the reason-side signal helps SAT so much more than UNSAT specifically?

We should keep this as possible project.

- Item 16: two 6-file samples agreed emphatically that `100` is wrong
  but disagreed on the exact best value in 1000–8000 — is "clearly
  better, exact optimum unresolved to the nearest thousand" good
  enough for a parameter that only matters when Luby is explicitly
  selected (not the default restart schedule), or does this merit a
  larger dedicated sweep before you'd trust `4000` specifically?

Probably not.  It is likely that it is a relatively flat hilltop.
Figuring out the exacty best value would take a lot of data and
not be much better than other values in the range.


- Per your own instruction, `REPORT48.md`'s two "further
  opportunities" (the append-on-watch-move cost, `isFalse`/
  `Literal.Var()`'s combined share) aren't acted on here — flagging
  them again now so they're on record for whenever the next
  brainstorming stage happens.

Include them in your mix of this stage. 
