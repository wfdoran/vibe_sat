# Stage 8

## Proprocessing

I like your first suggestion in REPORT7.md

1. **Preprocessing** (root-level unit propagation, pure literal
   elimination, subsumption, bounded variable elimination). This is
   explicitly pre-announced in `STAGE5.md` itself ("At a later Stage,
   we will add a Preprocessing step before starting any solving
   algorithm") and is the cheapest, lowest-risk item on this list: it
   benefits `hc`, `ws`, *and* `dfs` simultaneously, for free, by
   shrinking/simplifying the formula once before any algorithm sees
   it. Classic reference: Eén & Biere, ["Effective Preprocessing in
   SAT Through Variable and Clause
   Elimination"](https://www.cs.cmu.edu/~emc/15-820A/reading/een-biere-2005.pdf)
   (SatELite, 2005).

Let's implement this next.

We will use this with all algorithms.

## Command Line Parameters

The default will be that the preprocessing is run, but lets give the
user the ability to turn it off with either of these parameters.

--no-preprocessing      -x


