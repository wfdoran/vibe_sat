# Report: Stage 54

## What this stage was

A code review of the whole project (written by sonnet-5.0, reviewed here by
sonnet-5.5) in three phases: (1) find and fix bugs that crash or give wrong
answers, (2) find slow sections that can be sped up without changing behavior,
(3) list improvements not already in `REPORT50.md`. Every fix below has a
mirrored unit test in Go and Rust, and Go and Rust behave identically.

**Short version.** Five real bugs fixed (two of them wrong-answer bugs on
inputs with tautological or duplicate-literal clauses), four smaller
robustness fixes, and one large performance fix: bounded variable elimination
(BVE) on a 30,000-variable instance went from 23.5 s to 1.25 s (Go) and
17.6 s to 1.2 s (Rust) with byte-identical preprocessing results. Nothing has
been committed.

Coverage note, so you know how far to trust "no more bugs": I read the Go
sources in full for `cnf`, `cliargs`, `preprocess`, `hillclimb`, `dfs`,
`params`, `cmd/vibe_sat`, and the `cdcl` restart/import/analyze paths, and
the Rust mirrors of the same. I did **not** line-by-line audit Rust
`cdcl/` and `dfs/parallel.rs` beyond the mirrored fixes, or Go `cliargs/help.go`.
For those, confidence comes from the differential fuzzing below, not from
reading.

## Phase 1: bugs

| # | Bug | Effect | Status |
|---|-----|--------|--------|
| A | A clause containing both `x` and `-x` (tautology) breaks BVE resolvent computation | Wrong verdict / wrong reconstructed model | Fixed |
| B | A clause repeating a literal (`x x`) breaks watch selection: it looks like it has two distinct watchable literals when it is really a unit | Wrong verdict or missed propagation | Fixed |
| D | `ws`/`hc` with an empty clause in the input | Panic or infinite loop | Fixed |
| E | `dfs` `shedFrame` (work shedding to other threads) dropped assignments made at the root node | Latent: only reachable after ~1M nodes; a shed subproblem could search the wrong space | Fixed |
| F | `params` file with `glucoseWindowSize <= 0` | Panic (`makeslice` / index out of range) in **both** languages, in Go even when Glucose restarts are not selected | Fixed (validated at load) |

Smaller fixes:

- Go `--time-limit-secs` overflowed `time.Duration` for huge values
  (`timeLimitDuration`, saturating conversion, used at all 4 call sites).
- Go `parseByteSize` overflowed on e.g. `99999999999G`; Rust silently
  wrapped in release builds. Both now return an error
  ("... is too large to be a valid byte size").
- Go rejected any DIMACS line longer than 64 KB (scanner limit). The limit is
  now 1 GiB (`maxDIMACSLineBytes`).

### How A and B were fixed

`cnf.Normalize` / Rust `cnf::normalize` runs once right after the DIMACS is
read. It drops tautological clauses, removes repeated literals (first
occurrence kept), and leaves empty clauses alone (they mean UNSAT). It is
O(total literals). It prints
`normalized input: removed N tautological clauses and M duplicate literals`
at `-v 1` when it changed anything. I fixed it at the input boundary rather
than in each solver so every algorithm (`hc`, `ws`, `dfs`, `cdcl`) gets the
same clean input, and so the fix cannot drift between the two languages.

### Not a bug, but a design question (Bug C)

`--alg-params _` on its own satisfies the "`hc`/`ws` needs at least one
parameter" check, so the run proceeds with the default. I first read this as
a validation hole and fixed it, then found that STAGE44 makes `_` mean "use
the default" on purpose, and that two existing tests
(`TestParseUnderscoreAloneSatisfiesHCRequirement` and the Rust mirror) pin
that behavior. **I reverted my change** and left the behavior as it was.
Question 1 below.

### Evidence

- Bugs A, B and D were reproduced on the old binary before being fixed
  (tautology, duplicate-literal and empty-clause input files). Bug E is
  covered by a regression test (`TestShedFrameKeepsRootAssignment` and the
  Rust mirror); I did not demonstrate it end to end because it needs about
  a million search nodes to trigger.
- Differential fuzz (script kept in the session scratchpad, not in the
  repo): random small formulas (1-8 variables, 0-~50 clauses, including
  empty clauses, unit clauses, duplicates and tautologies) checked against a
  brute-force solver and an independent model checker, across ~32 solver
  configurations (dfs/cdcl/ws/hc, 1 and 3 threads, all restart modes, with
  and without preprocessing) in both languages. 360 instances before the
  BVE change and 240 more after it: **0 wrong answers, 0 crashes, 0
  Go-vs-Rust disagreements.**

## Phase 2: performance

### BVE re-evaluated rejected variables (fixed)

`eliminateVariables` / `eliminate_variables` restarts its scan from variable 1
after every successful elimination. Each restart re-resolved every variable
that had already been rejected ("would increase the clause count"), paying
the full resolvent cost again and draining the shared work budget on repeats
of the same rejection. A profile of the 30,000-variable instance showed 21.55 s
of 21.7 s inside this function.

Fix: a `rejected[v]` memo. Whether `v` is eliminable depends only on the live
clauses containing `v`, so the verdict stays valid until an elimination
removes or adds a clause that mentions `v`; at that point (every variable of
every removed clause and every new resolvent) the memo is cleared. It is
about 15 lines in each language.

Results (single runs, one thread, `-v 2`; times are wall clock including a 1 s
search limit for `big39`):

| Instance | Go before | Go after | Rust before | Rust after |
|---|---|---|---|---|
| synthetic 30000 vars / 117000 clauses | 23.48 s | 1.25 s | 17.58 s | 1.21 s |
| bw_large.d | 805 ms | 644 ms | 818 ms | 455 ms |
| bw_large.c | 192 ms | 148 ms | 96 ms | 64 ms |
| ssa6288-047 | 16.6 ms | 9.8 ms | 7.4 ms | 8.7 ms (noise) |
| uf250-01 | 203 ms | 204 ms | 197 ms | 191 ms |

The `preprocess:` summary line (variables, clauses, units, pure, subsumed,
eliminated) is **identical** before and after on the synthetic instance
(`eliminated=1080` both) and on 16 benchmark files in both languages: 0
differences. The rest of the existing brute-force-equivalence tests for BVE
still pass. Two new mirrored tests pin the behavior:
`TestEliminateVariablesDoesNotReEvaluateRejected` (exact budget spend of 7,
not 13) and `TestEliminateVariablesRejectionInvalidatedByNeighbour` (a
rejected variable becomes eliminable after a neighbour's elimination merges
two of its clauses, and must then be eliminated).

Honest caveat: on real benchmark files the gain is modest (up to about 1.8x
on `bw_large.d` in Rust) and the big win only shows on large instances with
many rejected variables. The synthetic instance is my own; I have not
measured a real 30k+ variable industrial file.

### Measured and left alone (negative results)

- CDCL `analyze` and `selectVar`: about 4% of runtime on 250-variable
  instances. `propagate` dominates and is already tuned. Not changed.
- Clearing the Glucose "recent LBD" window after a restart (Glucose proper
  does this): measured on 16 structured files, 10 s limit, Go. Without
  clearing: 26 restarts in 8,762 conflicts, 5.3 s total. With clearing:
  1 restart in 8,815 conflicts, 5.0 s total. On four random-3SAT `uf250`
  files, **zero** restarts happen either way in 53k conflicts. So at the
  tuned `glucoseK = 0.6` (REPORT35), "Glucose" is effectively "no restarts"
  on this corpus. Not a bug and not worth changing; noted so nobody is
  surprised. (Temporary instrumentation for this was removed; `cdcl.go` is
  byte-identical to HEAD.)

### Not done: bigger changes

- **`preprocess.Run` is still not covered by `--time-limit-secs`**
  (already in `todo.md`). The BVE fix shrinks the worst case I found from 23 s
  to 1.2 s but adds no guard: a differently shaped instance could still
  overrun. A cheap guard would be a deadline check in the BVE and subsumption
  loops.
- **VSIDS as a heap** instead of a linear scan for `selectVar`: only worth it
  for much larger instances than the benchmark set; at 4% it is not
  justified today.
- Small things I noticed but did **not** measure or change: Rust
  `eliminate_variables` clones `pos_idx`/`neg_idx` per candidate; `dfs`
  `SelectVar` allocates. Treat these as unmeasured guesses.

## Phase 3: improvements not in REPORT50.md

1. **Clause import cursor is never re-synchronized (by reading, not
   measured).** `maybeImport` (`clauseshare.go`) advances each peer cursor by
   exactly 1 per check (every 32 conflicts) and never clamps it to the peer's
   `writeIdx`. Two consequences: the import rate is at most one clause per
   peer per 32 conflicts, far below what peers publish, so sharing is much
   weaker than the design intends; and once a slow-exporting peer's ring has
   wrapped, the cursor can run ahead of `writeIdx` and re-read slots already
   imported, importing duplicates. Neither is a correctness problem. A cheap
   fix is to drain up to N clauses per check and clamp the cursor to
   `writeIdx`. It needs a benchmark to see whether more sharing actually
   helps; I have not run one.
2. **Range-validate the other internal parameters.** Only
   `glucoseWindowSize` crashes, so only that is validated now. But e.g.
   `varActivityDecay: 2.0` (and `0`) does not crash, yet uf250-01, which
   normally takes about 0.2 s, no longer reaches a verdict within 3 s.
   Rejecting decays outside (0, 1] and non-positive base conflict counts
   would turn silent degradation into a clear error. Zero/negative
   `lubyBaseConflicts`, `geometricGrowthFactor` etc. were tried and all
   still solved the instance, so they are lower priority.
3. **`.gitignore` hides a source directory.** Line 3 is `vibe_sat`, which
   matches the directory `go_src/cmd/vibe_sat/` as well as the binary. Only
   `main.go` is tracked (it was force-added); my new
   `go_src/cmd/vibe_sat/main_test.go` **does not show in `git status`**. You
   need `git add -f go_src/cmd/vibe_sat/main_test.go`, or anchor the pattern
   as `/vibe_sat` (root binary) plus `go_src/cmd/vibe_sat/vibe_sat`.
4. **Geometric restart threshold conversion** (`int(float64(base) *
   math.Pow(factor, n))`) is undefined in Go once the product exceeds int64.
   Unreachable in practice (roughly 94 geometric restarts already means about
   10^18 conflicts), so no action; noted for completeness. Rust saturates.
5. **Docs.** `docs/internal-parameters.md` line 39 should say
   `glucoseWindowSize` must be at least 1 (`docs/` is outside what this
   stage may edit).

## Files changed

Go: `internal/cnf/normalize.go` (+test), `internal/cnf/dimacs.go` (+test),
`internal/dfs/dfs.go` (+test), `internal/hillclimb/walksat.go` (+test),
`internal/preprocess/techniques.go` (+2 tests), `internal/cliargs/args.go`
(+test), `internal/params/params.go` (+test), `cmd/vibe_sat/main.go`
(+`main_test.go`, see item 3).
Rust: `cnf.rs`, `main.rs`, `cliargs.rs`, `hillclimb/walksat.rs`,
`dfs/mod.rs`, `dfs/parallel.rs`, `preprocess.rs`, `params.rs`, each with
mirrored tests.
No CLI flags or help text changed. The only new user-visible messages are the
`normalized input: ...` verbose line, the byte-size "too large" error, and
the `invalid <file>: cdcl.glucoseWindowSize must be at least 1` error.

Files in the working tree that are **not** from this stage:
`prompts/STAGE53.md`, `reports/REPORT53.md`, `prompts/STAGE54.md`,
`low_priority.md`, `todo.md`, the stray dotfiles, and the
`go_src/cmd/vibe_sat/vibe_sat` binary.

## Verification (final state)

- `gofmt -l .`: clean. `go vet ./...`: clean.
- `go test ./...` and `go test -race ./...`: all packages pass.
- `cargo fmt --check`: clean. `cargo clippy --release`: no warnings.
  `cargo test --release`: 284 passed, 0 failed.
- Fuzz and Go-vs-Rust preprocess-summary comparison as described above.
- All temporary files removed from the repo (`zz_tmp_*`, debug counters).

## Questions for you

1. **`-p _` alone for `hc`/`ws`:** keep STAGE44's "use the default"
   semantics (current behavior), or require at least one real value?
2. **`main_test.go` and `.gitignore`:** OK to fix the ignore pattern (I may
   not edit it), or will you `git add -f` the test file?
3. **Validate the other params (Phase 3, item 2)?** Only the crashing one is
   validated today.
4. **Time-limit guard for preprocessing:** worth a dedicated stage now that
   the biggest known overrun is fixed?
5. **Clause-import cursor (Phase 3, item 1):** worth a small benchmarked
   stage?
