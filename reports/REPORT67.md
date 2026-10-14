# Report: Stage 67

## What this stage was

`STAGE67.md` asked for a scheduled learned-clause database reduction with
Kissat-style tiers, replacing "reduce only when a memory limit is exceeded", and
a `-d` / `--database-toggle` flag to switch to the other policy. Your answers:
do the schedule and the tiers together; measure to pick the default; all threads
use the same schedule; an explicit memory limit stays as an additional trigger.
Both languages are done and behave identically. Nothing is committed. I did not
fetch or use the 2026 paper.

## 1. What was built

- **Schedule.** After `cdcl.reduceFirstConflicts` conflicts (default 2000), then
  after gaps of `reduceFirstConflicts + reduceIncrement × k` conflicts (increment
  default 300), `k` being the number of scheduled reductions so far. A value of 0
  for `reduceFirstConflicts` turns the schedule off. The memory limit
  (`--alg-params` val3) still triggers a reduction too.
- **Tiers.** A learned clause is never deleted if it is locked (a reason), or its
  LBD is at most `glueClauseLBDThreshold` (2; tier 1), or it is "recently used".
  A clause's use counter is set when it is learned and again each time it takes
  part in conflict analysis: to `tier2UsedReductions` (2) if its LBD is at most
  `tier2LBD` (6; tier 2), else to `tier3UsedReductions` (1). Each reduction
  decrements the counters, and only clauses at 0 are candidates. An imported
  (shared) clause is treated like a learned one, with its length as its LBD.
- **Deletion.** The worst `reduceFraction` (0.5, which was the fixed value before)
  of the candidates go, by LBD descending and then activity ascending, as before.
- **New internal parameters:** `reduceFirstConflicts`, `reduceIncrement`,
  `tier2LBD`, `tier2UsedReductions`, `tier3UsedReductions`, `reduceFraction`; in
  `params.go`, `params.rs`, `util/paramtune`, validated (ranges in `-h` and
  `docs/internal-parameters.md`).
- **`-d` / `--database-toggle`** (the letter `d` was free; taken were
  `i v a o t p x z c q h`). It overwrites the six values with the other policy's
  preset: if the schedule is on (`reduceFirstConflicts > 0`) it switches to the
  legacy preset (schedule off, `tier2LBD` = glue threshold, both counts 0,
  fraction 0.5), which reproduces the pre-Stage-67 behaviour; otherwise to the
  scheduled preset. It overrides what an internal-parameters file says for those
  six values, as documented in `-h`.
- The same schedule applies to every thread; each thread reduces its own private
  database. (Threads do not share one database; they exchange short clauses
  through export buffers. `docs/background.md`'s heading "shared clause database"
  is misleading and I left it unchanged.)

## 2. Measurements

PAR-2 is the sum over the files with an unsolved file counted as twice the limit,
so lower is better. Go numbers; Rust is within a few percent everywhere and
solved the same files. Samples are the Stage 66 ones: 8 files from each of
`uf250-1065`, `uuf250-1065`, `uuf175-753` (20 s limit, two seeds), and 8 files
from each of 5 structured directories (36 files, 30 s limit).

**One thread**

| Policy | Random 1 | Random 2 | Structured |
|---|---|---|---|
| Legacy (never reduces; `-d`) | 65 | 47 | 62 (35/36) |
| Legacy + 100 KB limit (`-p _ _ 100kb`) | **20** | **14** | 134 (34/36) |
| Scheduled, tiers (**the default**) | 36 | 26 | **13** (36/36) |
| Scheduled, no tiers (`tier2LBD`=2, counts 0) | 29 | 22 | 14 (36/36) |

**Four threads** (default portfolio): random 1 PAR-2 14 against 20 for the
legacy policy; structured 16 (36/36) against 62 (35/36).

- Any scheduled reduction is about **2x faster than the legacy default on random
  3-SAT** (3.2x for the 100 KB limit, as REPORT60 found) and solves `hole10` in
  about 11 s, which the legacy policy never does in 30 s.
- The 100 KB limit is the fastest on random 3-SAT but the **worst on structured
  instances**: it times out on `bw_large.d` and `hole10`, so it is not a safe
  default. This is the caveat REPORT60 raised.
- Tiers cost about 20% against no tiers on the random sets and tie on the
  structured one. I kept them: it is a small difference, and the tiers are what
  protect structured instances in principle.
- A small sweep around the default (sampled with the same files; all within the
  ~7% noise band of the default or of each other, all about 2x better than
  legacy): first/increment 1000/150 (PAR-2 30, 20, 12), 500/100 (26, 26, 15),
  no tier-3 protection (28, 25, 18), fraction 0.75 (32, 22, 12). No setting stood
  out, so the defaults stay 2000/300/6/2/1/0.5. I did not tune further.
- **Default.** Scheduled reduction with tiers. This changes the default
  behaviour. `-d` gives the old one.

## 3. Verification

- Go: `gofmt -l` clean, `go vet ./...` clean, `go test ./...` passes, and
  `go test -race ./internal/cdcl` passes. New `reduce_test.go`: tier protection
  and aging, the schedule's arithmetic and the off switch, a 150-formula
  brute-force fuzz with a reduction after every conflict (fractions 0.5 and 1),
  a pigeonhole UNSAT proof with reductions every few conflicts at 1 and 3
  threads, and the toggle presets. CLI and params tests extended.
- Rust: `cargo fmt --check` and `cargo clippy --release --all-targets -- -D
  warnings` clean, `cargo test --release`: 337 passed (332 before).
- `vibe_sat -h` is byte-identical between the two (`cmp`), and so is the file
  `-q` writes. `-d` runs in both and is slower on a UNSAT test file
  (2.9 s against 4.1 s in Go, 2.7 s against 4.0 s in Rust).
- **A problem from Stage 66 found and fixed:** `util/paramtune`'s
  `TestAllParamPathsCoversKnownFields` hard-codes the number of internal
  parameters and had been failing since Stage 66 added `vmtfThreadFraction`; I
  had not run that module's tests then. It now expects 23.
- Not rerun: the Stage 55 fuzz.

## 4. Notes

- Rust needed one extra `clause_used` vector and a params reference threaded
  through `analyze`, `add_learned_clause`, `reduce_clause_database`,
  `maybe_import` and `import_clause`; about 20 existing unit-test call sites got
  placeholder arguments, with the legacy policy for the old reduction tests.
- Kissat's version also refreshes a used clause's LBD and uses a square-root
  schedule; neither is done here.
- Not measured: larger or industrial instances (the SAT-competition files), longer
  limits, multi-thread with the fraction swept, and `vmtfThreadFraction` together
  with this change. The 2026 paper's lineage-based ranking is a possible later
  refinement for the candidates' order.
- `todo.md` and `low_priority.md` were not touched.
