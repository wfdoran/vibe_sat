# Stage 25

In this stage, I want you to take a good hard look at the preprocessor
performance.

## REPORT22.md Notes

2. **Parallelize preprocessing.** Your item, directly motivated by
   the Stage 21 finding above (`bw_large.c`/`.d`, `ssa7552-*`: ~11s
   single-threaded preprocessing vs. ~30ms of already-parallel `cdcl`
   search). Real, measured evidence this matters on real instances --
   but profiling should say *which* preprocessing step (unit
   propagation, pure-literal, subsumption, or BVE) is actually the
   cost before parallelizing the wrong one. My guess is BVE (pairwise
   clause resolution scales badly), but it's a guess.

You can now do this experiment. 

## REPORT24.md Notes

- **The Rust `preprocess` benchmark's slower-than-Go number** (above)
  is worth a real look once `REPORT22.md`'s item 2 (parallelize
  preprocessing) is picked up -- I did not investigate the cause this
  stage, since it wasn't this stage's scope and I didn't want to guess.

## Single Threaded

Look at both the rust and go implementations of the preprocessor.  Also,
investigate the Stage 24 finding the the rust preprocessor is slower
than the go preprocessor.  If you see any clear improvements, make the
change.

## Multi Threaded

When the user sets --num_threads > 1, try using this many threads to
speed up the proprocessing.

## Command Line Parameters

No changes to any of the command line parameters.


