# Report: Stage 60

## What this stage was

`STAGE60.md` asked for four things:

1. Fill in the new parameter ranges in `docs/internal-parameters.md`.
2. Preprocessing time: you decided it should **not** count against
   `--time-limit-secs`.
3. Quadratic unit propagation: "what can we do to fix this?"
4. A `paramtune` sweep over `rephaseIntervalRestarts`/`rephaseMaxFlips`, widened
   to every tunable parameter.

Nothing has been committed. Go and Rust behave identically.

**Headline results**

- Docs done (section 1). The preprocessing rule needed no code change (section 2).
- Unit propagation: I answered the question and did not implement it
  (section 3).
- Sweep: 15 parameters swept, plus follow-ups and confirmations. **Two defaults
  changed: `lrbAlpha` 0.4 to 0.1 and `glucoseK` 0.6 to 0.8** (section 4.3).
  Both held up on two independent file samples.
- Two parameters were not resolved: the **rephase pair** (flat everywhere I
  looked) and the **two preprocessing budgets** (the benchmark sample was too
  hard to measure them; section 4.5).
- The biggest finding is not a parameter. **A learned-clause memory limit made
  `cdcl` about 3x faster** on this benchmark set, with identical solved counts
  (section 5). That is a policy question for you.

## 1. Docs

`docs/internal-parameters.md` now has:

- a **Valid ranges** table for all 15 parameters, taken from `validate` in
  `go_src/internal/params/params.go` and checked against `rust_src/src/params.rs`;
- an explanation of the 1,000,000,000 cap (Stage 58);
- a section on preprocessing and `--time-limit-secs`;
- the stale "All 13 defaults" sentence corrected to 15, and the rows for the two
  changed defaults updated.

`docs/usage.md` gains a short paragraph under `--time-limit-secs` stating the
preprocessing rule. `docs/background.md` had two statements about the old
defaults (`glucoseK` 0.6, LRB `alpha = 0.4`); I updated them to match.

## 2. Preprocessing time

Current behaviour already matches your decision: the search gets the full
`--time-limit-secs`, and preprocessing runs on top of it, capped at one tenth of
the limit (Stage 57). So **no code changed**. Total wall time can reach 1.1x the
limit. `--help` already says this; I added it to `docs/usage.md` and
`docs/internal-parameters.md`. `TimeFraction` (10) is still a constant, not a
`.vibe_sat.json` parameter.

## 3. Quadratic unit propagation: what we can do

I did not implement this, because the question was "what can we do". The
analysis is from reading the code; I did not prototype it.

**Cause.** `UnitPropagate` (and `unitPropagateByDeadline`) repeat a full pass
over every clause (`simplifyWithAssignment`) once per round, and a chain
`x1, (-x1 or x2), (-x2 or x3), ...` needs one round per link. That is O(n) work
times O(n) rounds. The search bootstrap (`cdcl.newSolver`, `dfs` bootstrap)
calls the same function, which is why the Stage 57 time guard could not help
there.

**Fix: an occurrence-list worklist.**

1. Build a variable-to-clauses index once (O(total literals)).
2. Queue every unit clause. Pop a unit, assign it, and visit only the clauses
   containing that variable: drop the satisfied ones, remove the false literal
   from the others, and queue any clause that becomes unit. Detect a clause
   reaching zero literals as UNSAT.
3. Compact the clause list once at the end.

Each clause literal is touched a bounded number of times, so the whole thing is
O(total literals). Because the same function serves preprocessing and the
solver bootstrap, one rewrite fixes both places.

**Same idea for the other two.**

- *Pure literals:* keep per-variable positive and negative occurrence counts.
  When a clause is satisfied, decrement the counts of its other variables and
  queue any that become pure.
- *BVE restart scan:* instead of restarting at variable 1 after each
  elimination, keep a min-heap of variables whose occurrence lists changed. A
  min-heap pops the smallest-index candidate first, which should reproduce the
  current elimination order and so keep Go/Rust parity. I have not verified that.
  The m x m resolution cost of one hub variable is already capped by
  `bveWorkBudgetFactor`.

**Why behaviour should not drift.** The unit-propagation fixpoint and the
pure-literal fixpoint are both unique, so results should match the current code
apart from clause order. Clause order can change search tie-breaking, so the
exact trace of a solve could differ even though the verdict cannot.

**Cost and risk.** A real rewrite in both languages, with the existing fuzz tests
against a brute-force oracle as the safety net. Worth its own stage.

## 4. Parameter sweep

### 4.1 Tooling

- `util/paramtune`'s copy of the parameter table was missing
  `rephaseIntervalRestarts` and `rephaseMaxFlips`, so they could not be swept. I
  added them (and updated its test, 13 to 15 paths).
- I built the Go binary, the Rust release binary, `benchcompare` and `paramtune`
  into the scratchpad (the sandbox blocks the default Go cache) and ran
  everything from there.
- `paramtune` needs `--project-root` when run outside the repo.

### 4.2 Method

- One independent `paramtune` run per parameter (not coordinate descent, so the
  results do not compound). Each candidate is Go and Rust both, run sequentially
  by `benchcompare`, one thread, `--algorithm cdcl`, `--time-limit-secs 20`.
- **Sample 1:** 8 files from each of `uf250-1065`, `uuf250-1065`, `uuf175-753`
  (24 files, seed 1). Confirmations used seed 2 (and seed 3 for the geometric pair).
  Each seed draws a different file subset.
- `--alg-params` was set so the parameter is actually used: `_ 1` (Luby),
  `_ 3` (geometric), `3` (LRB), `_ 4` (Glucose), `_ _ _ 2` (rephase),
  `_ _ 500kb` for the clause-database parameters (see section 5).
- Ranking is `paramtune`'s own: unsolved count first, then summed elapsed time
  over both languages.
- **Noise.** The same default configuration (geometric 100 / 1.5, sample 1)
  measured 117.4 s, 117.5 s and 125.6 s in three runs, so run-to-run noise is
  about 7%. I treated differences under about 10% as ties, and wanted a change to
  show up on two samples before acting on it.

### 4.3 Results (sample 1, seed 1)

Times are summed elapsed seconds for 24 files, Go plus Rust. `unsolved` is the
count over both languages (so 48 runs).

| Parameter (default) | Candidates: total time (unsolved) | Outcome |
|---|---|---|
| `varActivityDecay` (0.95) | 0.9: 153.4 (1), **0.95: 128.3**, 0.97: 129.7, 0.99: 141.6 | Keep |
| `polynomialBaseConflicts` (18000) | 4500: 133.1, 9000: 136.0, **18000: 124.7**, 27000: 163.4 | Keep |
| `minimizeWorkBudgetFactor` (20) | 0: 142.0, 5: 129.5, **20: 128.6**, 100: 128.8 | Keep |
| `lubyBaseConflicts` (4000) | 1000: 160.7, 2000: 139.7, 4000: 135.8, **8000: 132.5** | Keep (2.4%, noise) |
| `geometricBaseConflicts` (100) | 50: 129.4, 100: 117.5, **200: 101.6**, 400: 113.3 | Not confirmed, keep |
| `geometricGrowthFactor` (1.5) | 1.2: 120.6, 1.5: 117.4, **2: 105.5** | Not confirmed, keep |
| `lrbAlpha` (0.4) | **0.2: 171.3 (0)**, 0.3: 177.4 (4), 0.4: 159.6 (9), 0.6: 70.4 (22) | **Change to 0.1** |
| `glucoseK` (0.6) | 0.5: 175.8, 0.6: 175.2, 0.7: 161.9, **0.8: 146.0** | **Change to 0.8** |
| `glucoseWindowSize` (50) | 25: 182.2, 50: 174.8, 100: 175.2, 200: 174.5 | Keep (flat) |
| `rephaseIntervalRestarts` (50) | 10 to 200: 123.6 to 124.4 | Inconclusive (4.4) |
| `rephaseMaxFlips` (1000) | 250: 123.6, 1000: 123.7, 4000: 123.4 | Inconclusive (4.4) |
| `clauseActivityDecay` (0.999), 500 KB limit | 0.99: 42.1, 0.995: 42.9, 0.999: 43.4 | Keep (flat) |
| `glueClauseLBDThreshold` (2), 500 KB limit | 0: 44.6, **2: 42.5**, 4: 62.4 (2), 6: 4.1 (24) | Keep |
| `subsumptionWorkBudgetFactor` (64) | 22 of 24 files unsolved | Inconclusive (4.5) |
| `bveWorkBudgetFactor` (2000) | 22 of 24 files unsolved | Inconclusive (4.5) |

For `glueClauseLBDThreshold=6`, 24 of 48 runs were unsolved; the 4.1 s total is
just the few that finished before the limit, not a speed-up.

**Follow-ups.**

| Sweep | Result |
|---|---|
| `lrbAlpha` 0.06 / 0.1 / 0.15 / 0.2 | 0.06: 124.2, **0.1: 122.2**, 0.15: 134.9, 0.2: 178.4. All solved. |
| `glucoseK` 0.8 / 0.9 / 1.0 | **0.8: 155.4**, 0.9: 187.2, 1.0: 240.0. Peak at 0.8. |

(The 0.2 value measured 171.3 s in the first sweep and 178.4 s here, again about
the 5 to 7% noise level.)

**Confirmation on sample 2 (seed 2), default vs candidate.**

| Parameter | Default | Candidate |
|---|---|---|
| `lrbAlpha` (`-p 3`) | 0.4: 142.7 s, **10 unsolved** | 0.1: **90.3 s, 0 unsolved** |
| `glucoseK` (`-p _ 4`) | 0.6: 143.0 s | 0.8: **120.9 s** (-15%) |

Both candidates agree in Go and Rust separately.

**The geometric pair did not confirm, so no change.** On sample 1 each value
looked better alone (base 200: -13.5%; growth 2: -10%). On sample 2, tested as a
pair, (200, 2) was best at 74.1 s against 91.0 s for the default pair (-19%), but
growth 2 alone with base 100 was *worse* (105.1 s). On two more samples:

| Sample | (100, 1.5) default | (100, 2) | (200, 1.5) | (200, 2) |
|---|---|---|---|---|
| 1 | 125.6 | 113.4 | **108.6** | 122.0 |
| 3 | 85.3 (4 unsolved) | 142.4 (2) | **79.1 (6)** | 127.9 (3) |

There is no consistent winner, and the two parameters interact. The geometric
defaults stay as they are.

### 4.4 The rephase pair

`rephaseIntervalRestarts` and `rephaseMaxFlips` are flat: all values within
about 0.7% under the default polynomial schedule. I suspected that schedule
restarts too rarely for rephasing to matter, so I re-ran both under the geometric
schedule (`-p _ 3 _ 2`), where restarts are much more frequent. Still flat:

| Sweep (geometric) | Result |
|---|---|
| interval 5 / 20 / 50 / 200 | 124.9 / 121.8 / 123.8 / 121.2 |
| max flips 250 / 1000 / 4000 | 121.2 / 121.3 / 120.8 |
| rephasing off (phase saving, `-p _ 3 _ 0`) | 121.8 |

Rephasing on is indistinguishable from rephasing off on this benchmark set. This
is the answer to the original REPORT43 question: at the first-guess defaults it was
a near-tie, and tuning does not move it. **I did not verify how many rephase
bursts actually fire in a run** (there is no verbose output for it, and I did
not instrument the source), so I can say it makes no measurable difference here,
not why. Random 3-SAT is also the least likely family for WalkSAT rephasing to
help, so a structured benchmark could still differ.

### 4.5 The preprocessing budgets: not measured

Both budgets need structured instances to matter, since random 3-SAT barely
preprocesses. The structured sets in `benchmark/` solve in under a second, so
nothing there could tell the candidates apart. I used a sample of
`sat_comp/2018` instead: first 12 files up to 4 MB (22 of 24 runs unsolved for
every candidate), then 20 files up to 1 MB (38 of 40 unsolved). With one file
solved per language, the sweep is just measuring that one file. **No conclusion
and no change.** Tuning these needs either a longer time limit on a handful of
files that are solvable, or measuring preprocessing output size and time directly
instead of end-to-end solve time. I did not try either.

### 4.6 Defaults changed

| Parameter | Old | New | Evidence |
|---|---|---|---|
| `cdcl.lrbAlpha` | 0.4 | **0.1** | Samples 1 and 2; fixes 9 to 10 unsolved files of 24 |
| `cdcl.glucoseK` | 0.6 | **0.8** | Samples 1 and 2 plus follow-up; 15 to 17% faster |

Both were applied to `go_src/internal/params/params.go`,
`rust_src/src/params.rs`, `util/paramtune/params.go`, the Rust default test, the
paramtune test and the docs. I checked `--reset-internal-params` writes
byte-identical files from both binaries.

**What this affects.** `lrbAlpha` is only used with `-p 3` (LRB), which is not a
default, so it changes nothing for default runs. `glucoseK` is used by
`-p _ 4` and also by the Glucose worker in the multi-thread round-robin restart
default, so the multi-thread default is affected too. I did not measure the
multi-thread case with the new `glucoseK`.

Note this partly reverses Stage 35, which moved `glucoseK` from 0.8 down to 0.6.
That sweep never tried values above 0.8, and the code has changed a lot since
then (Stages 45 to 51); on today's code and these samples 0.6 is slower. I have
not re-run the Stage 35 benchmark mix to see why the results differ. I have no sample
where 0.6 is better, but it is not a large sample.

## 5. A finding outside the parameter list: a clause memory limit is about 3x faster

The clause-database parameters (`clauseActivityDecay`, `glueClauseLBDThreshold`)
only matter when a learned-clause memory limit is set (`-p _ _ <limit>`), so I
swept them with `500kb`. The totals were three times lower than every other
sweep, so I measured it directly: same files, same defaults, only the limit varies.

| Limit | Sample 1 | Sample 2 |
|---|---|---|
| none (the default) | 133.4 s | 96.3 s |
| 2 MB | 72.5 s | 53.4 s |
| 500 KB | 47.9 s | 37.3 s |
| 100 KB | **41.4 s** | **30.1 s** |

All four settings solved all 24 files in both languages, so this is a speed
difference, not a completeness one. 100 KB is about 3.2x faster than no limit on
both samples.

This agrees with REPORT38, which found the clause-database reduction never runs
at all without an explicit limit. These instances are small (250 variables), so
an unbounded database makes propagation steadily slower for no benefit.

**Caveats.**

- The set is uniform random 3-SAT with at most 250 variables. A tight limit would
  probably hurt larger or structured instances that need to keep many learned
  clauses. I did not test any.
- It is a policy change (a default limit, or an automatic one scaled to the
  problem), not a parameter, so I did **not** change it.
- I did not measure multi-threaded runs.

## 6. Verification

- Go: `gofmt -l` clean, `go vet ./...` clean, `go test ./...` passes.
- `util/paramtune`: `go vet` and `go test` pass.
- Rust: `cargo fmt --check` clean, `cargo test --release`: 310 passed, 0 failed.
  I did not run `cargo clippy` this stage.
- End to end: rebuilt both binaries; `--reset-internal-params` output is
  identical; `-p 3` and `-p _ 4` solve `uuf250-01` in both languages (Go 5.8 s and
  6.9 s, Rust 5.5 s and 6.3 s).
- I did **not** rerun the Stage 55 differential fuzz or the benchmark checker.
  Only default parameter values changed, but I haven't proved that no test or
  script depends on the old values beyond the ones that failed and were fixed.
- All sweep output and the scripts that produced it are in the scratchpad, not
  the repo.

## 7. Open questions and notes for you

- **Memory limit.** Should `cdcl` get a default clause-memory limit, or
  something adaptive? It looks like the largest single speed lever found in a
  while, but I only tested small random instances.
- **Unit-propagation worklist.** Want it as its own stage (section 3)? It would
  also fix the Stage 57 unit-chain regression.
- **Preprocessing budgets.** Want a different measurement approach for them
  (section 4.5)? Or leave the defaults as they are.
- **Geometric restarts.** The sweep suggests the optimum is flat and noisy; it
  is only used by the multi-thread round-robin, so I left it.
- **Sample size.** Each sweep used 24 files. Noise is about 7%, so effects under
  about 10% should be treated as ties. The two changes made are large (15%+, and
  9 to 10 files unsolved vs 0).
- **Untracked benchmark changes.** `git status` shows `benchmark/notes.txt`
  modified and `benchmark/bf`, `benchmark/pigeon-hole`, `benchmark/pret` untracked.
  These are not mine.
- **Timing hygiene.** Sweeps ran sequentially with the machine otherwise idle
  except for light file edits; I did not pin CPUs.
