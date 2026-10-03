# Report: Stage 57

## What this stage was

From `STAGE57.md`:

1. **Preprocessing time guard.** With a time limit, preprocessing may use at
   most 1/10 of it. With no limit, it runs to completion.
2. **Profile.** Ideally every preprocessing subroutine is O(n log n). Note
   anything O(n^2) or worse, and maybe limit it.

Go and Rust behave identically. Nothing has been committed.

**Headline results**

- The guard works: on pathological inputs preprocessing now stops at about 2 s
  under `-t 20`, in both languages.
- It is **not** a free win. On the unit-propagation chain the guard turned a
  SAT answer into UNKNOWN, because the search repeats the same quadratic
  unit propagation (section 4).
- Unit propagation, pure-literal elimination, the BVE restart scan and the BVE
  hub are all O(n^2) or worse. Subsumption is budget-capped. Details and
  measurements are in section 3.

## 1. The time guard

**Design.** `main` computes `deadline = preprocessStart + limit/10`
(`TimeFraction = 10`; Go `preprocess.Deadline`, Rust `preprocess::deadline`).
No `-t` gives a zero `time.Time` (Go) or `None` (Rust), meaning no deadline.
`Run`/`run` takes the deadline as a new last argument. The search still gets
the full limit, so wall time can reach 1.1x the limit (see questions).

**Where it stops.** Only at points where the clauses and the assignment agree,
so an early stop leaves the formula less simplified but never wrong:

| Technique | Checked |
|---|---|
| Round loop | between techniques |
| Unit propagation (`unitPropagateByDeadline`) | after a whole simplify pass, before the unit scan assigns anything |
| Pure literals | after each complete fix-then-simplify pass |
| Subsumption (serial and parallel) | every 64 subsumers, counted from the chunk start |
| BVE | at each scan restart, every 64 variables, and in the resolve loop every 64 budget units; a half-resolved variable is not eliminated |

I did **not** reuse `UnitPropagate`'s existing `timeLimit`. It is meant for the
search bootstrap, which discards everything on a timeout. It aborts mid-scan,
which would leave the clauses and assignment inconsistent. The new function
checks only between whole passes.

A timed-out run sets `Stats.TimedOut` / `stats.timed_out` and, at verbosity 1,
prints `preprocess: time budget reached; stopped before reaching a fixpoint`
before the summary line. Pure-literal timeouts are reported conservatively
(even if the fixpoint was nearly reached).

CLI help (both languages) now says: with a time limit, preprocessing may use
at most one tenth of it; with none, it runs to completion.

Files: `go_src/internal/preprocess/{preprocess,techniques}.go`,
`go_src/cmd/vibe_sat/main.go`, `go_src/internal/cliargs/help.go`,
`rust_src/src/{preprocess,main,cliargs}.rs`, `rust_src/benches/preprocess_bench.rs`
(passes `None`).

## 2. Tests (mirrored Go and Rust)

Thirteen new Go tests in `deadline_test.go`, each with a Rust twin in
`mod tests` (snake_case names):

- `Deadline` with no limit (no deadline) and with a limit (one tenth), and
  `expired`.
- `unitPropagateByDeadline`: same result as `UnitPropagate` with no deadline
  or a far one; stops with clauses and assignment consistent when expired.
- Pure literals stop after one pass when expired. Subsumption (serial and
  parallel) and BVE do nothing and report timed out when expired.
- `Run` with an expired deadline stays equivalent to the input formula and
  sets `TimedOut`; with a far deadline it matches `Run` with none.
- `TestRunStopsPromptlyOnLongUnitChain`: a 60,000-link chain with a 20 ms
  deadline returns in far less than a full run (Go and Rust).

## 3. Profile: complexity per technique

Single thread, release builds, one run per cell (so +-10% noise). Sizes double
each row; a linear routine roughly doubles, a quadratic one roughly
quadruples. "Run" is the whole pipeline.

The "unit chain" is `x1, (-xi or xi+1)`. The "pure chain" is `(xi or -xi+1)`.
The "BVE hub" is m clauses `(x, a_i, b)` and m clauses `(-x, -a_i, -b)`, whose
resolvents are all tautologies. The "xor path" appears only in the end-to-end
test in section 4.

### Go, before the guard (Stage 56 code)

| Input | n | unitprop | pure | subsume | bve | Run |
|---|---|---|---|---|---|---|
| rand3 r=4.26 | 25k / 200k | 10ms / 26ms | 10ms / 65ms | 21ms / 383ms | 70ms / 1.28s | 196ms / 2.7s |
| rand3 r=1.5 | 25k / 200k | 1ms / 10ms | 7ms / 54ms | 5ms / 102ms | **842ms / 86s** | 10ms / 75ms |
| rand3 r=30 (dense) | 1k / 8k | 1ms / 6ms | 0 / 6ms | 10ms / 185ms | 8ms / 115ms | 60ms / 297ms |
| unit chain | 5k / 40k | **48ms / 3.25s** | **99ms / 8.3s** | 1ms / 6ms | 38ms / 2.5s | 45ms / 2.9s |
| pure chain | 5k / 40k | 0 / 1ms | **69ms / 4.25s** | 0 / 7ms | 47ms / 2.4s | 63ms / 4.2s |
| BVE hub | m=500 / 4000 | 0 | 0 | 0 / 1ms | **10ms / 310ms** | 9ms / 312ms |

### Rust (same harness, same generators)

| Input | n | unitprop | pure | subsume | bve | Run |
|---|---|---|---|---|---|---|
| rand3 r=4.26 | 25k / 200k | 9ms / 20ms | 10ms / 58ms | 24ms / 407ms | 85ms / 1.50s | 153ms / 3.0s |
| rand3 r=1.5 | 25k / 200k | 1ms / 8ms | 6ms / 56ms | 5ms / 92ms | **826ms / 78s** | 7ms / 121ms |
| rand3 r=30 (dense) | 1k / 8k | 1ms / 6ms | 1ms / 8ms | 9ms / 154ms | 8ms / 80ms | 18ms / 247ms |
| unit chain | 5k / 40k | **45ms / 2.9s** | **67ms / 4.3s** | 0 / 4ms | 27ms / 1.6s | 42ms / 2.7s |
| pure chain | 5k / 40k | 0 / 1ms | **33ms / 2.1s** | 0 / 4ms | 26ms / 1.6s | 33ms / 2.1s |
| BVE hub | m=500 / 4000 | 0 | 0 | 0 / 1ms | **6ms / 361ms** | 6ms / 361ms |

Rust and Go agree on every growth rate. Rust is ~1.5-2x faster on the chains.

### Findings

**Quadratic or worse (verdicts from the growth between the first and last row):**

1. **Unit propagation on implication chains.** 8x the size gave ~68x the time
   (Go; ~4x per doubling, so O(n^2)). Cause:
   `simplifyWithAssignment` is a full pass over all clauses and the unit loop
   repeats it once per link. Linear on random 3-SAT.
2. **Pure-literal elimination on chains.** Same shape and same cause: one full
   pass per newly pure variable.
3. **BVE restart scan.** After each elimination the scan restarts at v=1 and
   walks all variables, so k eliminations cost O(k * num_vars). The
   `rand3 r=1.5` row shows it: 86 s at 200k variables (Go, calling BVE
   directly). **Through `Run` this is masked** (75 ms), because pure-literal
   removal clears those variables first. Only inputs with many eliminable
   variables that are neither pure nor unit expose it. The chain rows' `bve`
   column (2.5 s at 40k) is the same effect.
4. **BVE hub.** m positive times m negative resolution attempts per variable:
   O(m^2). The shared budget (`bveWorkBudgetFactor` x clauses) caps it, so it
   cannot run away, but a single hub variable can use the whole budget.

**Budget-capped, so bounded but not O(n log n):** subsumption worst case
(`subsumptionWorkBudgetFactor`) and the BVE hub above. Both already have
explicit limits.

**Roughly linear / O(n log n):** random 3-SAT at r=4.26 and r=30 for every
technique. Two are visibly superlinear and I did not investigate them further:
BVE at r=4.26 took 18x the time for 8x the size (Go 70 ms to 1.28 s; Rust 85 ms
to 1.50 s), about n^1.4, which fits the restart scan in item 3 starting to
matter. Subsumption on the dense r=30 family took 18x for 8x the size (Go).
Neither is quadratic at these sizes, but neither is clearly O(n log n).

**Minor constant factor (not a complexity issue):** Rust BVE clones
`pos_idx`/`neg_idx` per candidate variable.

### What I did about them

Only the guard. The time guard bounds the wall time of items 1-3 inside
preprocessing. I did **not** rewrite any of the quadratic routines: STAGE57
says to note them and "maybe" limit them, and an O(n) propagation queue or an
O(1)-amortized BVE restart is a larger change that deserves its own stage.
The existing budgets already limit item 4 and subsumption.

## 4. End-to-end check of the guard (`-t 20`, so a 2 s preprocessing deadline)

60,000 variables, `cdcl`, one run each, wall clock including the search:

| Instance | Stage 56 Go / Rust | Stage 57 Go / Rust |
|---|---|---|
| unit chain | 6.4 s SAT / 5.8 s SAT | **22.0 s UNKNOWN / 22.0 s UNKNOWN** |
| pure chain | 7.9 s SAT / 4.5 s SAT | 2.06 s SAT / 2.03 s SAT |
| xor path (XOR chain) | 5.6 s SAT / 2.5 s SAT | 2.06 s SAT / 2.04 s SAT |

- **Pure chain and xor path:** preprocessing stops at ~2 s. The search then
  finishes immediately on the remaining formula. Faster and still correct.
  (The xor path is solved by BVE one variable at a time, so it exercises the
  BVE deadline; `eliminated` was 32,860 / 43,330 of 59,999 when it stopped.)
- **Unit chain: a negative result.** Preprocessing stopped at ~2 s with about
  10k of 60k units propagated. The search then got a chain of ~50k links and
  could not finish in its 20 s. Cause: the CDCL bootstrap (`cdcl.newSolver`)
  calls the same `UnitPropagate`, regardless of `-x`, so the quadratic work is
  merely moved, not removed. I confirmed by timing the search alone with `-x`:
  `cu5000` 0.42 s, `cu10000` 1.6 s, `cu20000` 6.5 s (Go; Rust 0.40/1.57/6.3 s).
  So at this size, with a limit, the guard gives UNKNOWN where the old code
  answered SAT, because the old preprocessing happened to finish inside the
  limit. Without `-t` nothing changes.
- The total wall time is 22 s for `-t 20` (preprocessing 2 s, then the search's
  full 20 s). This is the "search still gets the full limit" design. Say if you
  want preprocessing time subtracted instead.

## 5. Verification

- Go: `gofmt -l` clean, `go vet ./...` clean, `go test ./...` passes,
  `go test -race ./internal/preprocess` passes. `util/` was not changed.
- Rust: `cargo fmt --check` clean, `cargo clippy --release --all-targets --
  -D warnings` clean, `cargo test --release`: 310 passed, 0 failed.
- A run with no `-t` is meant to be unchanged; the unit tests check that
  `Run` with no deadline matches the old behaviour, but I did not diff whole
  benchmark runs.
- I did **not** rerun the Stage 55 differential fuzz or the 726-run benchmark
  checker against the Stage 57 binaries (their stored outputs predate this
  stage). The tests above check soundness at every stop point, but the stop
  points are only exercised end to end by the three instances in section 4.
- The temporary profiling harnesses (a Go test and a Rust `#[ignore]` test) were
  removed; `git status` shows only the intended files plus your `low_priority.md`,
  `todo.md`, `prompts/STAGE57.md` and the editor lock files for `STAGE58.md`.

## 6. Open questions and known gaps

- **Fix the unit-propagation quadratic?** It dominates items 1-2 and is also in
  the search bootstrap, so a guard alone can't fix it (section 4). An
  occurrence-list queue makes it O(n) and would fix the unit chain in both
  places. Want that as a stage?
- **Same for pure literals** (a worklist instead of a full pass per link) and
  the **BVE restart** (resume the scan instead of restarting at v=1).
- **Should preprocessing time count against the limit?** Now the search gets the
  whole limit, so total wall time is up to 1.1x.
- **Small limits:** `-t 1` gives only 0.1 s of preprocessing. Fine, but is the
  1/10 fraction right? `TimeFraction` is a constant, not a tunable parameter.
- **Conservative `TimedOut`:** the pure-literal check can report a timeout
  even if it was about to reach the fixpoint.
- **Only three instances** test the stop points end to end; the BVE
  resolve-loop stop is covered only by unit tests.
