# Report: Stage 66

## What this stage was

`STAGE66.md` asked for Kissat-style mode switching: a new `--alg-params` val1 = 5
that toggles between VMTF and VSIDS, with a different restart strategy for each,
plus a multi-threaded variant that splits the threads between the two. It also
asked two questions that, if answered "yes", meant stopping before any
implementation. Both languages are done and behave identically. Nothing is
committed.

## 1. The two gating questions

**Geometric rather than quadratic with VSIDS? No.** I measured VSIDS with Luby,
quadratic and geometric restarts (8 files from each of 3 random directories,
twice with different seeds, plus 5 structured directories, Go and Rust):

| PAR-2 (Go; Rust within noise) | Luby | quadratic | geometric |
|---|---|---|---|
| random sample 1 (24 files, 20 s) | 71 | 64 | 61 |
| random sample 2 (24 files, 20 s) | 57 | 46 | 44 |
| structured (36 files, 30 s) | 66 | 62 | 69 |

Geometric is about 5% better on random sets (inside the ~7% noise band) and about
10% worse on the structured set. That is a tie, so quadratic stays.

**A better way to implement switching?** One finding changed the design, and you
chose it: VMTF with Luby (the proposal's pairing) was the worst VMTF
combination (15/24 and 18/24 solved on the random sets, against 19/24 with
quadratic). You chose Glucose restarts for VMTF, as Kissat's focused mode uses.
That is what is implemented. I did not measure VMTF with Glucose on its own before
building it.

## 2. What was built (val1 = 5)

- **One thread:** starts *focused* (VMTF, Glucose restarts). At every restart it
  flips between focused and *stable* (VSIDS, quadratic restarts). The `val2` value
  is ignored; the announcement prints `restart=auto`.
- **Several threads:** each thread keeps one mode. `round(fraction × threads)`
  threads, chosen at random, are focused (VMTF + Glucose); the rest are stable
  (VSIDS + quadratic). The count is clamped to leave at least one of each.
  `val2` is ignored; `val4` (phase) still rotates as before.
- **New internal parameter** `cdcl.vmtfThreadFraction`, default `0.5`, accepted
  range 0 to 1, in `params.go`, `params.rs`, `util/paramtune` and
  `docs/internal-parameters.md`, so it can be swept.
- **Only the active mode's structure is updated**, as Kissat does: VSIDS
  activity is bumped and decayed only while stable, VMTF stamps only while
  focused. The VMTF cursor is kept valid during stable stretches, because
  backtracking still runs.
- **Quadratic schedule index:** counts only finished stable stretches, so the
  quadratic interval grows at the speed of VSIDS restarts rather than twice as
  fast. Glucose's moving averages keep updating in both modes.
- `val1` now accepts 0 to 5; the error text says "0, 1, 2, 3, 4, or 5".
- Docs: `usage.md`, `internal-parameters.md`, `background.md`, `references.md`
  (Chanseok Oh, SAT 2015, from memory, not re-checked), and `-h` in both
  languages (byte-identical, checked with `cmp`).

## 3. Measurements

Same files and seeds as the section 1 grid. PAR-2 counts an unsolved file as twice
the limit; all numbers are Go, Rust is within ~5%.

| Single thread | VSIDS + quadratic (default) | VMTF + quadratic | **val1 = 5** |
|---|---|---|---|
| random sample 1 | 24/24, 64 | 19/24, 285 | 24/24, **84** |
| random sample 2 | 24/24, 46 | 19/24, 271 | 24/24, **53** |
| structured | 35/36, 62 | 35/36, 64 | **36/36, 14** |

| 4 threads | default portfolio | val1 = 5 |
|---|---|---|
| random sample 1 | 24/24, 18 | 24/24, **38** |
| structured | 35/36, 62 | 35/36, 62 |

- **Random 3-SAT:** switching is worse than plain VSIDS, by about 30% single-thread
  on sample 1 (84 vs 64), 15% on sample 2 (53 vs 46), and about 2x with 4 threads.
  Single-thread it is far better than plain VMTF.
- **Structured:** the whole single-thread gain is **one file**, `hole10.cnf`
  (pigeonhole): switching proved it UNSAT in 12.4 s, where VSIDS timed out at 30 s.
  Everything else in that set is a tie. With 4 threads, neither the default nor
  val1 = 5 solved `hole10` in 30 s. This is one instance, so it shows switching
  can help on some structured UNSAT problems, not that it generally does.
- **Not measured:** other `vmtfThreadFraction` values (you said you will sweep
  that later), longer limits, and the large competition-style instances.

So val1 = 5 is **not** a better default on this project's benchmarks, and I did
not change the default.

## 4. Verification

- Go: `gofmt -l` clean, `go vet ./...` clean, `go test ./...` passes (new
  `switch_test.go`: mode flipping and schedule index, VMTF cursor during stable
  mode, 150-formula brute-force fuzz with very frequent flips, pigeonhole UNSAT,
  thread assignment counts/clamping/determinism/randomness, and multi-thread SAT and
  UNSAT at fractions 0, 0.5, 1). CLI and params tests updated.
- Rust: `cargo fmt --check` clean, `cargo clippy --release --all-targets -- -D
  warnings` clean, `cargo test --release`: 332 passed (327 before), with the
  same tests ported.
- `vibe_sat -h` and `-q`'s `.vibe_sat.json` are byte-identical between Go and
  Rust. Running both binaries with `-p 5` (one and four threads) prints
  `select_var=5 restart=auto` and solves; `-p 6` is rejected identically.
- Not rerun: the Stage 55 fuzz, `go test -race`.

## 5. Notes

- To keep Rust's `maybe_restart` free of mode knowledge, it gained one argument
  (`schedule_index`); the four existing unit-test calls pass `0`.
- Which threads are focused is drawn from the run's RNG, so the Go and Rust
  assignments differ for the same seed, as other per-thread randomness already does.
- `todo.md` and `low_priority.md` were not touched.
