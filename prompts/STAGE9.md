# Stage 9

## Watched Literals

Let's implement watched literals next to speed up BCP.

2. **Watched literals for BCP.** The current `BCP`/`bcp` rescans the
   occurrence lists built in Stage 2; the standard, much faster
   technique is to "watch" only two not-yet-falsified literals per
   clause and only re-examine a clause when one of its watched
   literals changes. This is an internal data-structure upgrade, not
   a new algorithm, but it's the engineering foundation that makes
   everything below it (especially CDCL, which calls BCP *far* more
   often, once per conflict) practical at scale. Reference: Moskewicz,
   Madigan, Zhao, Zhang & Malik, ["Chaff: Engineering an Efficient SAT
   Solver"](https://www.cs.princeton.edu/~zkincaid/courses/fall22/readings/SATChaff.pdf),
   DAC 2001.

## Command Line Parameters

I don't see any reason to make this optional.  So no changes to the
command line.  The watched literal version will be the only version of
BCP.
