# Report: Stage 55

## What this stage was

Two things from `STAGE55.md`:

1. Fix the clause-import cursor in CDCL clause sharing (REPORT54 Phase 3
   item 1) and **benchmark** whether more sharing helps.
2. Apply your answer to REPORT54 Question 1: for `hc` and `ws`, the user
   must give either a time limit or a real iteration count; if neither is
   given, that is an error. So `-p _` alone no longer satisfies the check.

(Questions 2-4 were already answered: `.gitignore` you fixed, parameter
validation is a later stage, the preprocessing time guard is in `todo.md`.
I did none of those.) Go and Rust behave identically. Nothing has been
committed.

**Short version.** The fixed cursor makes the 8-thread `uuf250` runs about
35% faster (geomean wall clock, both languages) than the old one, and about
2.3x faster at 32 threads. But the gain comes from *keeping the cursor in sync with the
peer*, not from importing *more*: reading 1, 8, 32 or 256 clauses per peer
per check were all within noise of each other. I picked 8.

## 1. The import cursor

### The problem

`maybeImport` read one clause per peer per check (every 32 conflicts) and
did `peerCursors[i]++`. It never looked at the peer's `writeIdx`. So:

- The cursor fell ever further behind a fast-learning peer; what it read was
  progressively staler.
- Once a peer's 256-slot ring wrapped, the cursor pointed at slots that had
  already been overwritten, so it read newer clauses out of order and could
  read the same clause more than once.

Neither is a correctness problem (the sharing contract is lossy by design),
but both make sharing weak.

### The fix

A pure helper, `importWindow` (Go) / `import_window` (Rust):

```
importWindow(cursor, written, capacity, maxPerPeer) -> (start, end)
  end   = written
  start = min(cursor, end)
  start = max(start, written - capacity)   // saturating: skip overwritten slots
  start = max(start, end - maxPerPeer)     // bound work per check
```

`maybeImport` reads `[start, end)` from each peer, imports what it reads
(`importClause` still drops a clause that has no two non-false watchable
literals), and sets `cursor = end`. So the cursor can never run ahead of the
peer and never falls permanently behind. A backlog larger than
`importMaxPerPeer` keeps only its newest clauses (the same lossy trade the
ring makes). Work per check is bounded by `numPeers * importMaxPerPeer`.
`ExportBuffer.published()` (`writeIdx.Load()`) was added for this.

Known, harmless race: a peer can overwrite a slot between `published()` and
the read, so the read returns a newer clause that is imported again on the
next check. Rare; a duplicate, not a wrong answer. It is in the doc comment.

Files: `go_src/internal/cdcl/clauseshare.go`,
`rust_src/src/cdcl/clause_share.rs`. Constant: `importMaxPerPeer = 8` /
`IMPORT_MAX_PER_PEER = 8`.

### Benchmark

Set: the `uuf250-1065` UNSAT instances (search-bound; preprocessing is
negligible on them). The `uf250` SAT instances solve in ~100 ms and are
uninformative, so I did not use them to measure. Variants (old behavior,
and N = 1, 8, 32, 256 clauses per peer per check) were interleaved per
instance to cancel machine drift. Every run was checked: **0 non-UNSAT
verdicts in all sweeps.** Geometric mean wall-clock seconds:

| Run | old | N=1 | N=8 | N=32 | N=256 |
|---|---|---|---|---|---|
| Go, 8 threads | 2.874 | 1.865 | **1.846** | 1.944 | 1.827 |
| Rust, 8 threads | 2.736 | 1.873 | **1.802** | 1.920 | 1.923 |
| Go, 4 threads | 3.329 | 2.675 | **2.533** | 2.618 | 2.735 |
| Go, 32 threads | 5.546 | 2.786 | **2.426** | 2.587 | 2.576 |
| Rust, 32 threads | 5.168 | **2.284** | 2.429 | 2.554 | 2.654 |

Reading the table honestly:

- Every new variant beats the old behavior, by 20% (Go, 4 threads, N=1) to
  56% (Go, 32 threads, N=8). That is well outside the noise.
- N=1 already gets most of the gain. N=1 differs from the old code only in
  that it re-synchronizes the cursor to the peer. So the benefit is from the
  re-sync, and "drain more" adds little or nothing.
- N=1/8/32/256 are within noise of each other: per-cell spread between
  repeated runs was 0.09-0.29 s, as large as the differences between
  variants. I chose 8 because it had the best average rank across the five
  runs (0.594 vs 0.616 for N=1, 0.625 for N=32, 0.628 for N=256), not
  because it is demonstrably better. Treat that choice as a coin flip
  between 1 and 8.
- Rust at 32 threads shows a monotone slowdown as N grows (2.28, 2.43, 2.55,
  2.65). It is the only run with a visible trend, and it is the opposite of
  "more sharing helps". Go at 32 threads does not show it. I did not
  investigate why.
- Caveat on scope: one benchmark family (`uuf250`), 6-10 instances per run,
  2-3 repetitions. I did not measure structured instances (`ssa`,
  `bw_large`); REPORT21 noted those are preprocessing-bound, so a sharing
  change would not show up there anyway.

## 2. `hc` / `ws` require a time limit or an iteration count (Q1)

`cliargs` validation now rejects `hc` or `ws` unless `--time-limit-secs` is
given **or** the first `--alg-params` value is a real number (not `_`). The
first value is the number of starts (`hc`) or tries (`ws`).

| Command | Before | Now |
|---|---|---|
| `-a hc -p _` | accepted (default) | error |
| `-a ws -p _ 1000` | accepted | error |
| `-a hc -p _ -t 5` | accepted | accepted |
| `-a hc -p 10` | accepted | accepted |
| `-a ws -p 5 _ 50` | accepted | accepted |
| `-a ws -t 5` | accepted | accepted |
| `-a hc` (nothing) | error | error |

Error text (identical in Go and Rust): `for --algorithm=hc, either
--time-limit-secs or a real (not "_") first --alg-params value (number of
starts) must be given` (`ws` says "tries"). Help text for `--time-limit-secs`
and `--alg-params` was updated to say so. I ran both final binaries to
confirm the messages and accept/reject behavior match.

The old tests that pinned `-p _` alone as valid
(`TestParseUnderscoreAloneSatisfiesHCRequirement` and the Rust mirror) were
replaced by tests of the new rule.

**Behavior change to be aware of:** any script that ran `hc`/`ws` with only
`-p _ ...` and no `-t` will now fail with that error. The `_` semantics for
all other positions and algorithms (STAGE44) are unchanged.

I did not look for or edit docs outside the source tree; `grep` for the
semantics in repo Markdown found nothing besides `STAGE55.md` and the
Go/Rust sources, but if you keep README/docs text about `-p _` or the hc/ws
requirement elsewhere it will need updating (I cannot edit it).

## 3. Tests (mirrored Go and Rust)

Cursor fix:

- `TestExportBufferPublished` / `test_export_buffer_published`.
- `TestImportWindow` / `test_import_window`: 11-case table covering nothing
  published, caught up, small backlog, ring not yet wrapped, overwritten
  slots, cursor inside the surviving range, the per-check cap (smaller than,
  larger than, and equal to one), a cursor ahead of the peer, and a cursor
  far ahead after a wrapped ring.
- `TestMaybeImportDrainsBacklogAndResynchronizesCursor`,
  `TestMaybeImportNeverReadsPastOverwrittenSlots`,
  `TestMaybeImportClampsCursorAheadOfPeer`,
  `TestMaybeImportRespectsCheckGate`, and the Rust `test_maybe_import_*`
  equivalents (using an `ImportFixture` helper).

CLI:

- `TestParseHCWSRequireRealIterationCountOrTimeLimit`, `TestHasIterationCount`
  and the Rust `test_parse_hc_ws_require_real_iteration_count_or_time_limit`,
  `test_has_iteration_count`.

## 4. Verification

- Go: `gofmt -l` clean, `go vet` clean, `go test ./...` passes,
  `go test -race -count=2` on `cdcl` and `cliargs` passes (earlier run was
  `-count=3`).
- Rust: `cargo fmt --check` clean, `cargo clippy --release -D warnings`
  clean, `cargo test --release`: 291 passed, 0 failed.
- **Benchmark verdicts with an independent checker** (final binaries, both
  languages, parallel `cdcl` at 2, 8 and 16 threads): 726 runs over
  `uf250` (SAT), `flat200` (SAT), `uuf175` and `uuf250` (UNSAT) and the
  `blocksworld` files except `bw_large.c/.d`. SAT answers were checked
  against the original CNF clause by clause; UNSAT answers were checked
  against the known label. **0 wrong, 0 crashes.** (My first attempt at this
  checker flagged everything as wrong because it read the trailing
  `%` / `0` of the `uf250` files as an empty clause; that was a checker bug,
  fixed, and the solvers were not at fault.)
- **Differential fuzz**: random small formulas against a brute-force solver
  and the same model checker, over every dfs/cdcl restart mode, with and
  without preprocessing, plus new `cdcl -z 4` and `-z 8` configurations, `hc`
  and `ws` with `-t 1`, both languages: 50 instances, **0 mismatches**.
  These formulas are too small to reach an import (it needs 32 conflicts), so
  the fuzz guards against breakage elsewhere; the import path itself is
  covered by the unit tests and the 726 benchmark runs above.
- Not done: no separate Go-vs-Rust output diff on SAT models, because models
  legitimately differ between runs of a parallel portfolio. Each is verified
  against the CNF instead.

## 5. Things to know

- The tool sandbox in this session created zero-byte read-only placeholder
  files in the repo root (`.bashrc`, `.bash_profile`, `.gitconfig`,
  `.gitmodules`, `.idea`, `.mcp.json`, `.profile`, `.ripgreprc`, `.vscode`,
  `.zprofile`, `.zshrc`, and files under `.claude/`). They all have the same
  timestamp and are not mine; `git status` lists them as untracked. Do not
  `git add` them; they are probably safe to ignore or delete.
- The temporary binaries, benchmark scripts and fuzz script live in the
  session scratchpad, not in the repo. `git status` shows only the eight
  intended modified source files, plus your `low_priority.md`,
  `prompts/STAGE55.md` and `todo.md`.

## 6. Open questions

1. **N=8 versus N=1?** Statistically a tie. If you prefer the simplest
   possible rule, N=1 plus the resync is the minimal fix. I left 8.
2. **Rust slows down with N at 32 threads.** Worth a look if you plan to run
   32-thread Rust? It could be cache traffic from reading more peers'
   buffers, but I did not measure that.
3. **Different benchmark families.** All the evidence is on random 3-SAT
   UNSAT. A structured UNSAT family (not preprocessing-bound) would tell us
   whether importing more helps there.
