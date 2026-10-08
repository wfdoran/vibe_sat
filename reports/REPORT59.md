# Report: Stage 59

## What this stage was

`STAGE59.md` asks for item 10 of `REPORT50.md`: a dedicated multi-threaded
benchmark of phase round-robin's diversity value. `REPORT43.md` made round-robin
(`-p _ _ _ 3`) the default phase strategy whenever `-z > 1`, but only
single-threaded runs of each strategy were measured. The multi-thread default
was justified by analogy. This stage is a measurement; **no source file was
changed**, and nothing has been committed. The two languages are untouched, so
they remain identical.

Round-robin gives worker `i` the strategy `[saving, target, rephase][i % 3]`.
So `-z 4` runs saving, target, rephase, saving; `-z 8` runs s,t,r,s,t,r,s,t.

## Short answer

- On this machine round-robin is **never the best phase setting overall, and
  never clearly the worst**. Against the best single-strategy configuration it is
  within noise on mean time.
- Its value is as a **hedge**: the three strategies are good at different
  things, and round-robin gets most of each one's strength.
  - On satisfiable random 3-SAT, `target` (1) is clearly the fastest and
    `saving` (0) and `rephase` (2) are 2x slower.
  - On unsatisfiable random 3-SAT, `target` is clearly the slowest (about
    1.25x behind `saving`) and the other three are close.
  - Round-robin is second on both SAT and UNSAT (by mean time), so it never
    pays `target`'s UNSAT penalty and recovers most of `target`'s SAT gain.
- Round-robin's gain over `saving` (the single-thread default, i.e. "no
  diversity") is **small**: about 0.8x geometric-mean time at z=4, with a mean
  difference of about 0.3 s on a 7.6-7.9 s mean. At z=8 it is not distinguishable
  from zero overall.
- I recommend **keeping the default**. The data do not justify changing it,
  and no alternative beats it overall. I did not try to tune the mix (see the
  last section).

## Method

**Driver.** A scratch Python script (not added to the repo) runs
`vibe_sat -a cdcl -z Z -t T -v 1 -p _ _ _ PHASE -i FILE` one process at a time
and records wall-clock time, verdict and, for SAT, whether an independent model
checker (own CNF reader, parses the `v` lines) accepts the model. Jobs are
shuffled and the order of the four phases within each instance is shuffled
(seeded), so drift in machine load cannot favour a phase. Runs were strictly
sequential, never concurrent. The machine has 12 cores, so `-z 4` and `-z 8`
leave cores idle.

**Metrics.** Solved count; PAR2 (unsolved counts as 2 x the limit, equal to the
mean here because almost everything was solved); mean and median time. Pairwise
comparison is on **per-instance means** (averaged over repetitions) with a
bootstrap over *instances* (5000 resamples, 95% interval) for both the mean
difference and the geometric-mean time ratio. Repetitions of one instance are
correlated, so bootstrapping over runs would give intervals that are too
narrow; I used the per-instance version for every number below. "Geo ratio < 1"
means the first-named setting is faster.

**Correctness checks.** All 2,504 runs agree on every verdict (no instance was
SAT in one run and UNSAT in another) and every SAT model verified.

### Instance sets

| Set | Instances | Why |
|---|---|---|
| A | `uf250-1065` + `uuf250-1065` (200, 100 SAT / 100 UNSAT) | REPORT43's family |
| B | 48 random 3-SAT, n = 280, ratio 4.26, seeds 5900..5947 | harder; 25 SAT / 23 UNSAT |
| E | 7 blocksworld + 8 ssa + first 30 `flat200-479` | structured |

Set B is generated, not shipped (the benchmark directory is read-only for me):
for `i` in 0..47, `random.Random(5900 + i)`, `m = round(4.26 * 280)` clauses,
each from `sample(range(1, 281), 3)` with a random sign per literal.

### Runs

| Run | Binary | z | limit | reps | Set | Runs |
|---|---|---|---|---|---|---|
| A | Go | 4 | 30 s | 1 | A | 800 |
| B | Go | 4 | 60 s | 1 | B | 192 |
| C | Go | 4 | 60 s | 2 | B | 384 |
| D | Go | 8 | 60 s | 2 | B | 384 |
| E | Go | 4 | 30 s | 2 | E | 360 |
| F | Rust | 4 | 60 s | 2 | B | 384 |

B and C are pooled below (3 repetitions per instance and phase).

## Results

### A: uf250 / uuf250, z=4 (null)

| Phase | Solved | Mean (s) | Median (s) |
|---|---|---|---|
| saving | 200 | 1.60 | 1.11 |
| target | 200 | 1.73 | 1.06 |
| rephase | 200 | 1.56 | 1.16 |
| round-robin | 200 | 1.65 | 1.12 |

Every setting solves every instance (longest run 21 s) and the mean
differences are within +-0.1 s. This family is **too easy at z=4 to say
anything**; pooled over SAT and UNSAT, the settings are indistinguishable.
(Run A used one repetition and the per-run paired comparison, so its intervals
are the optimistic kind; I do not lean on them.) This is why I moved to a
harder set.

### E: structured, z=4 (null)

All 90 runs per phase solved; every instance takes under 0.6 s
(largest: `bw_large.d`, about 0.5 s), and the means are 0.04-0.05 s for all four
phases. **These instances cannot discriminate phase strategies at all**; the
test says nothing about structured problems that are hard.

### B + C: n=280 random 3-SAT, Go, z=4, 3 repetitions (48 instances)

| Phase | Mean (s) | Median (s) | Solved of 144 |
|---|---|---|---|
| saving | 7.86 | 5.64 | 144 |
| target | 8.56 | 4.61 | 143 |
| rephase | 8.78 | 6.50 | 143 |
| round-robin | 7.61 | 5.94 | 144 |

Pairwise (per-instance bootstrap, 95% interval):

| Comparison | Mean diff (s) | Geo time ratio |
|---|---|---|
| rr vs saving | -0.25 [-0.88, +0.34] | 0.88 [0.76, 1.02] |
| rr vs target | -0.96 [-2.36, +0.11] | 1.17 [1.01, 1.38] |
| rr vs rephase | -1.17 [-2.53, -0.20] | 0.81 [0.70, 0.94] |
| saving vs target | -0.71 [-2.26, +0.58] | 1.33 [1.10, 1.62] |

Only "rr beats rephase" excludes zero on both measures. Round-robin vs saving is
a tie on mean time and a probable small win on geometric mean.

**Split by satisfiability** (the verdict seen in the runs):

| | SAT (25) mean | UNSAT (23) mean |
|---|---|---|
| saving | 3.47 | 12.63 |
| target | **1.71** | 16.02 |
| rephase | 4.05 | 13.92 |
| round-robin | 2.73 | 12.91 |

- SAT: target beats saving by 1.76 s [0.87, 2.74] (geo ratio 2.17x). Round-robin
  is 1.03 s slower than target [0.30, 1.70] but 0.73 s faster than saving
  [-1.56, +0.05] and 1.32 s faster than rephase [0.37, 2.37].
- UNSAT: target is 3.4 s slower than saving [1.4, 5.9] and 3.1 s slower than
  round-robin [1.5, 5.5]. Round-robin vs saving is +0.28 s [-0.66, +1.03] but a
  geo ratio of 1.08 [1.01, 1.15]: a small, probably real, slowdown.

The lone `target` and lone `rephase` timeouts (143 of 144 solved each) are
both on `r280-18`, an UNSAT instance that sits at 40-60 s for every setting. It
has no bearing on the comparison, but it is why PAR2 for target and rephase is a
little above their mean.

### D: n=280, Go, z=8, 2 repetitions

| Phase | Mean (s) | Median (s) |
|---|---|---|
| saving | 6.96 | 5.62 |
| target | 7.39 | 4.45 |
| rephase | 6.80 | 3.97 |
| round-robin | 6.81 | 4.45 |

All pairs involving round-robin include zero on mean difference
(rr vs saving -0.15 [-1.08, +0.70]; rr vs rephase +0.01 [-1.04, +0.95]). The
SAT/UNSAT shape repeats: target is best on SAT (1.75 vs 3.77 for saving) and
worst on UNSAT (13.5 vs 10.4); round-robin on SAT is 2.50 (1.27 s faster than
saving [0.07, 2.73]) and on UNSAT 11.49 (1.07 s slower than saving
[0.20, 2.05]). The z=8 mix is s,t,r,s,t,r,s,t (3 saving, 3 target, 2 rephase), and
the hedge is again what shows, not an overall win. Going from z=4 to z=8 only
shrinks the means by about 10-23% on this set (2 repetitions at z=8 against 3
at z=4), so the portfolio scales poorly here with every phase setting.

### F: Rust, z=4, 2 repetitions (Go-vs-Rust coverage)

One round-robin run is excluded; see the next section. Without it:

| Phase | Mean (s) | Median (s) |
|---|---|---|
| saving | 8.00 | 5.75 |
| target | 9.21 | 3.79 |
| rephase | 8.33 | 5.31 |
| round-robin | 8.03 | 4.79 |

The same pattern as Go: SAT means saving 3.79, target 1.77, rephase 4.39,
round-robin 2.36; UNSAT means 12.58, 17.29, 12.62, 14.20. Round-robin vs saving
on SAT is -1.43 s [-2.71, -0.34]; vs target on UNSAT it is -3.09 s
[-6.37, -0.94]. Overall, round-robin is level with saving
(+0.03 s [-1.51, +2.15]).

## One unexplained outlier

In run F, one Rust round-robin run on `r280-04` (a SAT instance) finished with
**`el = 1296.7 s` under `-t 60`**, with verdict SAT. The model was valid. Its
neighbours in the same job list took normal times (15 s, 1.3 s, 13 s). I could
not reproduce it: 48 reruns of the same command (`-z 4 -t 60 -p _ _ _ 3`) all
finished in under 25 s, and none of the 2,120 Go runs exceeded 60.02 s. The wall
clock gave no hint (`dmesg` shows nothing, uptime is 11 days, and the 72-minute
job total equals the sum of the recorded times including this run). I cannot
say whether it was the host stalling (a suspend, or the process being
stopped) or a Rust time-limit or shutdown bug that happens rarely. **I did not
investigate further and I did not change code.** If it is a Rust bug, the
likeliest place is the parallel time-limit/stop path, and it would be worth a
stress loop (thousands of short runs with `-t`) in a separate stage. For the
statistics above I dropped that single run (leaving one repetition for that
instance and phase); with it kept, Rust round-robin's mean becomes 21.45 s, which is
entirely this one record.

## Caveats

- One machine, 12 cores, no CPU pinning; the other applications on the
  machine were not controlled, though runs were interleaved to spread drift.
- 48 random instances, one size and ratio. Random 3-SAT near threshold is not
  representative of industrial SAT. The structured set I tried was too easy to
  tell anything. A hard structured benchmark (e.g. bounded model checking,
  hardware verification) is what would settle whether round-robin matters for
  real use; I did not have one.
- 25 SAT and 23 UNSAT instances: the SAT/UNSAT split intervals are wide, and
  heavy tails (`r280-04` takes 0.4 s or 23 s depending on the run) mean a few
  instances dominate the means.
- z=2 (saving + target) and z=3 (saving, target, rephase) were not measured.
  z=12 and above were not measured.
- Only phase varies. Restart strategy also defaults to round-robin when
  `-z > 1` in all of these runs, so these results measure phase diversity on top
  of restart diversity (the same as the shipped default), not phase diversity
  alone.
- At z=4 the round-robin mix is s,t,r,s: two saving workers, one each of the
  others. A different mix could do better; I did not search for one.

## Answer to the stage's question

REPORT43's multi-thread default is **supported, weakly**. Round-robin does not
dominate any single phase strategy, and compared with plain saving its edge is
small (a tie on mean time at both z=4 and z=8; a probable 12-26% geometric-mean
gain in Go and Rust at z=4, and 18% at z=8 with an interval that just touches 1). What it reliably does is avoid
`target`'s clear UNSAT penalty (about 25% slower) while keeping a good share of
its SAT advantage (about 2x on satisfiable instances). If the typical workload
is mostly satisfiable, `target` alone would be faster; if mostly unsatisfiable,
`saving` alone would be as fast. Without knowing the mix, round-robin is a
reasonable default. I made no change.

## Verification

No source files changed, so the usual build/test checks were not needed. I
checked that every verdict agreed across all runs, that all SAT models verified
with an independent checker, and that the Go and Rust binaries used were the
Stage 58 builds. Temporary files were in the scratchpad and are not in the repo.
