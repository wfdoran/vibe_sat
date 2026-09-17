# Stage 31

I am happy with your solution to subsumption slowness.


Now that BVE is the slow part of preprocessing, let's look at that.
Ideally, find a faster algorithm.  If not, limit the work in some way.

From REPORT30.md

- **BVE's rebuild-per-eliminated-variable cost (above) is now this
  project's most urgent known performance problem** — more urgent than
  anything left over from `REPORT29.md`, since it's what's actually
  responsible for the unchanged 7/15 and 6/15 timeout counts in this
  stage's own reproduction of that report's Finding 4. I'd recommend this
  as the next stage, with the same shape of investigation this stage and
  `REPORT25.md` used: profile first, then fix, in both languages, with a
  fuzz test against a brute-force oracle.


As you suggest: profile, fix, fuzz test.  This seems like a good
general strategy.



Side note: please do not alter todo.md.  That is where I am keeping
notes for myself.