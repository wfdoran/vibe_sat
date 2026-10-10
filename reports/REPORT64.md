# Report: Stage 64

## What this stage was

`STAGE64.md` asked for three things, as a first step toward Kissat-style
stable/focused mode switching (`REPORT63.md` item 1):

1. Add **VMTF** (Variable Move-To-Front) as a variable-selection heuristic for
   `cdcl`: `--alg-params` `val1 = 4`.
2. For the threaded version, add it "into the mix".
3. A quick survey of 5 CDCL variable-selection algorithms.

Done in both languages. Nothing has been committed. Go and Rust behave
identically.

**Headline results**

- VMTF works and is verified (section 1). Its decision step is dramatically
  cheaper than VSIDS or LRB: **about 5 ns against 39 µs at 50,000 variables**
  (section 4).
- But as a *search* heuristic on this project's benchmarks it is **clearly
  weaker on random 3-SAT** (19 of 24 solved against 24 of 24, and the SAT
  files take 3.6x as long) and **equal on the structured set** (section 3).
- The threaded "mix" (section 2) is **20% to 50% slower than all-VSIDS on the
  random set** and equal on the structured set. I implemented it the way you
  asked and made it the default, but you may want to flip it (section 2).
- Side finding: with `lrbAlpha` now 0.1 (Stage 60), **LRB is the best single
  heuristic on the random set** (section 3).

## 1. VMTF

### How it works here

- The variables sit in a doubly linked list ordered by a stamp that grows toward
  the front. A decision takes the **first unassigned variable from the front**.
- **Bumping.** After each conflict, `analyze` collects the variables it touched
  (the same set VSIDS bumps) and, after the first-UIP loop, moves each to the
  front, **oldest first**, so the group keeps its previous relative order.
  Level-0 variables are never bumped. A variable already at the front is left
  alone.
- **A search cursor** remembers where the last decision stopped. The invariant is
  "every variable newer than the cursor is assigned". Backtracking moves the
  cursor forward whenever a newer variable becomes unassigned, so each variable is
  skipped at most once per descent: O(1) amortized per decision. No scores,
  decay, or rescaling.
- **Initial order.** Variable 1 at the front, so before any conflict decisions
  walk the variables in increasing index order, the same starting point as the
  static heuristic and as the other heuristics (all scores 0). (CaDiCaL and Kissat
  may start from the other end; I am not sure, and chose this for consistency
  with our other heuristics.)
- **Phase** is independent of the heuristic: it still comes from the phase
  strategy (saving, target, rephase).

Go: `cdcl.go` (`SelectVarVmtf`, `initVmtf`, `vmtfBump`, `vmtfBumpAnalyzed`,
`selectVarVmtf`, hooks in `analyze`, `backtrackTo`, `decide`, `newSolver`).
Rust: `cdcl/mod.rs` (`VmtfState`, `SelectVarVariant::Vmtf`, hooks in `analyze`,
`backtrack_to`, `maybe_restart` and the decide step; the new state is threaded
through as one extra parameter).

### The CLI

- `--alg-params` `val1`: **4 = VMTF**, **5 = round-robin** (section 2). Valid range
  is now 0 to 5, with the same error text in both languages. `--help` documents
  both, in both languages.
- VMTF takes no tuning parameters, so nothing was added to `.vibe_sat.json`.

### Tests (mirrored)

- List integrity and initial order; bump from the middle, from the back, and of the
  front variable (no restamp); `bump_analyzed` keeps relative order.
- The cursor: decisions skip assigned variables; after a decision, a bump while
  assigned, a backjump and a restart, the next decision is the newest unassigned
  variable. Includes a helper that checks the invariant directly.
- `analyze` on the hand-verified conflict used for the VSIDS test: the variables
  involved end up ahead of the untouched one.
- **A fuzz test: 150 random small 3-SAT formulas per heuristic against exhaustive
  enumeration**, with restarts, for both VMTF and the round-robin choice; every
  reported model is checked.
- Pigeon-hole proofs with and without restarts; the round-robin mapping and the
  coprime-period property; a 5-thread run on an UNSAT and a SAT formula.
- Go: `go_src/internal/cdcl/vmtf_test.go` (also run under `-race`). Rust: tests
  inside `cdcl/mod.rs`. Counts: Go cliargs/cdcl tests updated; **Rust 328 pass**
  (318 before).

## 2. The threaded mix

There was no per-worker heuristic mix before this stage (every worker used the
same `val1`), so "add it into the mix" meant creating one, the way the restart
and phase strategies already work:

- **`val1 = 5` is the round-robin choice**, and is the **default with
  `--num-threads` above 1** when no `val1` is given (like `val2 = 5` and
  `val4 = 3`). Worker `i` uses `[VSIDS, VMTF, LRB, VSIDS, VMTF][i % 5]`. With one
  thread it means VSIDS.
- **The period is 5, on purpose.** The restart rotation has period 4 and the phase
  rotation period 3. `REPORT45.md` raised the problem of a third rotation:
  period 3 would travel with the phase strategy (worker 3 would always pair the
  same heuristic with the same phase), and you said "wait for it" and that
  something random might be wanted. Period 5 is coprime to both, so across 60
  workers every (heuristic, restart, phase) combination occurs, with no
  randomness needed. If you still want random assignment, it is a small change.
- Weighted and static are left out of the rotation (they are only for
  comparison). VSIDS and VMTF get two slots each; LRB one.

### What the mix costs (measured)

`cdcl`, 24 random files (8 each of `uf250`, `uuf250`, `uuf175`), 20 s limit. All
files solve in every row, so the times below are total seconds for the 24 files:

| Threads | All-VSIDS (`-p 2`, the old default) | Mix (new default) | Slower by |
|---|---|---|---|
| 4, Go | 20.8 s | 25.3 s | 22% |
| 4, Rust | 16.8 s | 21.2 s | 26% |
| 8, Go | 15.7 s | 23.9 s | 52% |
| 8, Rust | 15.0 s | 21.6 s | 44% |

The sign agrees across all four comparisons. On the 36-file structured set at 4
threads the two are equal (about 62 PAR-2, identical solved counts). This is one
run per cell and the absolute differences are 4 to 9 seconds, so I would not trust
the exact percentages, but the direction is consistent and matches section 3: a
slot given to VMTF or LRB is a slot not given to VSIDS, and random UNSAT is
VSIDS's best case.

**Decision for you.** I followed your instruction (VMTF is in the threaded
default), but the data say it does not help on these benchmarks and costs 20% to
50% on random ones. The old behaviour is one flag: `--alg-params 2`. If you want
the default changed back to all-VSIDS and `val1 = 5` kept as an opt-in, it is a
two-line change in each language's `main`. I did not, since you asked for the
mix. A fair counterpoint: the benchmarks here are the ones VSIDS is tuned for
(uniform random 3-SAT), and the diversity argument for a portfolio is about
instance families we do not have.

## 3. Survey of the five heuristics

`cdcl`, single thread, otherwise default settings (so polynomial restarts, phase
saving, `lrbAlpha = 0.1`). Both languages, one run per cell.

**Random set:** 24 files (8 `uf250` SAT, 8 `uuf250` UNSAT, 8 `uuf175` UNSAT), 20 s.

| Heuristic | Solved Go / Rust | PAR-2 Go / Rust | SAT files (8): Go seconds | UNSAT files: solved |
|---|---|---|---|---|
| **3 LRB** | 24 / 24 | **61.1 / 57.9** | 13.1 | 16 of 16 |
| **2 VSIDS** | 24 / 24 | 65.8 / 62.0 | 12.3 | 16 of 16 |
| **4 VMTF** | 19 / 19 | 288.9 / 282.1 | **43.8** | 11 of 16 |
| 0 weighted | 12 / 13 | 506.3 / 462.7 | 8.7 (4 solved) | 8 of 16 |
| 1 static | 8 / 8 | 670.8 / 669.5 | 0.1 (1 solved) | 7 of 16 |

**Structured set:** 36 files (blocksworld, ssa, 8 each of flat125, flat200, and
`pigeon-hole` hole6 to hole10), 30 s.

| Heuristic | Solved | PAR-2 Go / Rust | Notes |
|---|---|---|---|
| 2 VSIDS | 35 / 36 | 62.1 / 61.8 | only `hole10` unsolved |
| 4 VMTF | 35 / 36 | 63.7 / 63.2 | `hole9` 2.3 s against VSIDS's 0.8 s; `hole10` unsolved |
| 3 LRB | 35 / 36 | 69.6 / 68.8 | UNSAT files total 8.4 s against 1.0 s |
| 1 static | 35 / 36 | 63.9 / 63.6 | unsolved: `bw_large.d`; **solves `hole10`** |
| 0 weighted | 34 / 36 | 145.0 / 136.1 | unsolved: `hole10`, `bw_large.d` |

(PAR-2 counts an unsolved file as twice the limit, so the 62-ish values on the
structured set are almost entirely that one unsolved file.) There were **0
Go-versus-Rust verdict mismatches** across every run in this report.

**Reading it**

- **VSIDS and LRB are the two good heuristics on random 3-SAT**, and LRB is now
  slightly ahead. That reverses `REPORT13.md`'s result, and the likely reason is
  Stage 49 and Stage 60 (reason-side bumping, and `lrbAlpha` going from 0.4 to
  0.1): we have not re-run the old comparison to confirm it. It is one run on 24
  files, so the gap is within noise; "LRB has caught up" is the safe reading.
  LRB is clearly slower on structured UNSAT in this sample (8.4 s against 1.0 s,
  8 files).
- **VMTF is clearly weaker on random 3-SAT.** It leaves 5 of the 16 UNSAT files
  unsolved in 20 s, and its SAT files take 3.6x as long (43.8 s against 12.3 s,
  8 files). That is the expected shape: random 3-SAT has no locality for a
  recency-based order to exploit.
- **On structured instances VMTF is level with VSIDS**, except for `hole9`.
- **Weighted and static are weak on random**, as they were in `REPORT13.md`.
  Static solving `hole10` (which neither VSIDS, VMTF nor LRB did in 30 s) is a
  curiosity: pigeon-hole's natural variable order suits "lowest index first". It
  is one file, one run.

## 4. Decision cost, measured directly

The search comparison cannot show VMTF's real selling point (cheap decisions on
big formulas), because on the instances that solve, propagation dominates. I
tried 12 SAT Competition files with 20,000 or more variables (30 s, Go): **11 of
12 were unsolved by both heuristics**, and the only file either solved was solved
by VSIDS (23.0 s), not VMTF. So I have *no* search-level evidence on large
instances.

What I could measure is the decision step alone: a Go benchmark that selects and
assigns variables one at a time and resets, over a solver with N variables
(`BenchmarkDecision*` in `vmtf_test.go`):

| Variables | VSIDS (linear scan) | VMTF |
|---|---|---|
| 1,000 | 1,535 ns | 4.6 ns |
| 10,000 | 6,760 ns | 1.7 ns |
| 50,000 | 38,947 ns | 5.2 ns |

So VSIDS's cost per decision grows with the number of variables, as expected from
the linear scan `selectVarByActivity` does, and VMTF's does not. Whether that
matters overall depends on how large a fraction of a conflict's cost the decision
step is; I did not measure that. (It also suggests VSIDS and LRB themselves could
be made much cheaper with a heap, which is a separate, existing-code improvement
I did not make.)

## 5. Verification

- Go: `gofmt -l` clean, `go vet ./...` clean, `go test ./...` passes,
  `go test -race ./internal/cdcl ./internal/cliargs` passes.
- Rust: `cargo fmt --check` clean, `cargo clippy --release --all-targets --
  -D warnings` clean, `cargo test --release`: **328 passed**.
- `util/paramtune` tests pass.
- End to end: VMTF (`-p 4`), the explicit round-robin (`-z 3 -p 5`) and the
  threaded default (`-z 4`) give the right verdicts in both languages on
  `uf250-01`, `uuf250-01`, `hole8`, `bw_large.d` and `ssa2670-141`, and every
  verdict in the survey agreed between languages.
- **What I did not check.** I compared *verdicts* between Go and Rust, not the
  decision and conflict counts. Single-threaded VMTF has no randomness, so the two
  should in principle search identically, but the solver does not print those
  counts, and I did not instrument it. I also did not rerun the Stage 55 fuzz.

## 6. Notes and caveats

- **Sample sizes.** 24 and 36 files, one run per cell. Treat differences under
  about 10% as noise (`REPORT60.md`).
- **The survey harness.** The `.vibe_sat.json` in the scratch directory it ran from
  (left over from Stage 61) only sets `bvaWorkBudgetFactor` to 0, which is the
  default, so it changed nothing; the run output says "loaded from .vibe_sat.json".
- **Attribution.** I credit VMTF to L. Ryan's Siege solver (2004) and compare with
  Biere & Froehlich (SAT 2015) in the code comments and `docs/references.md`. Both
  are **from memory and not re-checked**; the docs and comments say so. (I first
  wrote a different attribution in the comments and corrected it before finishing.)
- **Docs updated:** `docs/usage.md` (the `val1` row and the round-robin
  explanation), `docs/background.md` (a new section on the five heuristics),
  `docs/references.md`.
- The `val1` range check in the CLI changed from 0 to 3 to 0 to 5 in both
  languages, and the tests for it were updated.

## 7. What this sets up, and open questions

- This is the first piece of `REPORT63.md` item 1. Mode switching would use VMTF
  in a "focused" phase and VSIDS in a "stable" one. This stage gives it the VMTF
  half and shows that on our benchmarks VMTF alone does not beat VSIDS, so the
  payoff would have to come from the switching and the different restart and phase
  policies in each mode.
- **Question 1:** keep the round-robin default for `-z > 1`, or revert it to
  all-VSIDS (section 2)? It costs 20% to 50% on random sets here.
- **Question 2:** the earlier "seed the VMTF order from a structural score"
  discussion is not done. It would be a one-pass sort of the initial list.
- LRB now looks as good as VSIDS on random 3-SAT; if you want, a larger
  re-comparison (with more files and repetitions) could decide whether the
  single-thread default should change. I did not touch it.
