# Report: Stage 68

## Summary

`STAGE68.md` asked for a benchmarking run like `REPORT53.md`'s: `vibe_sat`'s
`cdcl` and `dfs` against `minisat` and `cryptominisat5` on the same problems.
This is that run on the current tree (through `STAGE67.md`, uncommitted), with
Go and Rust for both of `vibe_sat`'s algorithms. No code was changed.

**Headline: on the 250-variable random instances `vibe_sat`'s `cdcl` now matches
`minisat`, and on `huck.col.11` it beats both external solvers.** All 26 files
and all six solver columns finished; every definite verdict agrees across
solvers (0 mismatches).

- `uf250-0100` (SAT): Go 2.13 s, Rust 1.98 s, against `minisat` 2.59 s and
  `cryptominisat5` 3.19 s. `vibe_sat` was 1.9x slower than `minisat` at Stage 53
  and is now slightly faster.
- `uuf250-0100` (UNSAT): Go 3.81 s, Rust 3.70 s, `minisat` 3.72 s,
  `cryptominisat5` 9.28 s. Stage 53 had `vibe_sat` 2.7x slower than `minisat`.
- `huck.col.11` (UNSAT): Go 7.5 s, Rust 7.1 s, `cryptominisat5` 16.5 s, `minisat`
  32.6 s. At Stage 53 it took `vibe_sat` 44 s and 41 s.
- The worst remaining gap is `uf250-03` (SAT), 2.1 s and 2.0 s against
  `minisat`'s 0.44 s (about 4.5x; it was about 10x at Stage 53).

## Method

As in `REPORT40.md` and `REPORT53.md`: the same 26 files (11 SATLIB random,
`uf250-03`, 5 structured, 9 industrial from `sat_comp/2018`), a 60-second
wall-clock cap per solver per file (`timeout 60`), runs strictly sequential,
and cleaned copies for `minisat` and `cryptominisat5` only (SATLIB's trailing
`%`/`0` removed from the `benchmark/` files, tabs turned into spaces); `vibe_sat`
ran the original files. A `timeout 3` parse check of all 26 cleaned files against
both external solvers passed before the timed sweep.

Solvers: `vibe_sat --algorithm=cdcl` and `--algorithm=dfs`, each in Go and Rust,
`--num-threads 1`, release builds of the current tree with default settings
(this includes Stage 67's scheduled clause-database reduction, now the default);
`minisat` 2.2.1 with default settings; `cryptominisat5` 5.8.0 with `-t 1 --verb
0`. The machine, the files and the external solvers' versions are the same as in
`REPORT53.md`. One measurement per cell, not repeated.

A first and a second attempt at this sweep were discarded: a shell bug made every
exit code read as 0 (timeouts and `minisat` verdicts were mislabelled), and a
stray earlier run was still going when I restarted, so two sweeps overlapped.
The numbers below are from a third, clean sweep with nothing else running.

## Results

Wall-clock seconds; `TIMEOUT` = hit the 60 s cap.

| File | go cdcl | rust cdcl | go dfs | rust dfs | minisat | cryptominisat5 |
|---|---|---|---|---|---|---|
| uf20-01 (SAT) | 0.005 | 0.004 | 0.005 | 0.004 | 0.007 | 0.011 |
| uf50-01000 (SAT) | 0.007 | 0.006 | 0.008 | 0.007 | 0.007 | 0.012 |
| uf75-0100 (SAT) | 0.012 | 0.008 | 0.015 | 0.010 | 0.008 | 0.011 |
| uf100-01000 (SAT) | 0.009 | 0.009 | 0.025 | 0.014 | 0.008 | 0.013 |
| uf175-0100 (SAT) | 0.073 | 0.061 | 0.175 | 0.068 | 0.017 | 0.032 |
| **uf250-0100 (SAT)** | **2.130** | **1.979** | 43.77 | 25.81 | 2.585 | 3.185 |
| **uf250-03 (SAT)** | **2.110** | **1.982** | 15.81 | 5.386 | 0.439 | 1.804 |
| uuf50-01000 (UNSAT) | 0.004 | 0.006 | 0.009 | 0.006 | 0.006 | 0.011 |
| uuf75-0100 (UNSAT) | 0.008 | 0.007 | 0.014 | 0.008 | 0.007 | 0.012 |
| uuf100-01000 (UNSAT) | 0.013 | 0.012 | 0.032 | 0.021 | 0.010 | 0.016 |
| uuf175-0100 (UNSAT) | 0.079 | 0.063 | 0.712 | 0.220 | 0.028 | 0.082 |
| **uuf250-0100 (UNSAT)** | **3.811** | **3.704** | TIMEOUT | 24.22 | 3.715 | 9.279 |
| anomaly (SAT) | 0.005 | 0.005 | 0.007 | 0.006 | 0.007 | 0.011 |
| medium (SAT) | 0.009 | 0.007 | 0.009 | 0.008 | 0.007 | 0.011 |
| bw_large.a (SAT) | 0.019 | 0.014 | 0.032 | 0.026 | 0.011 | 0.009 |
| ssa0432-003 (UNSAT) | 0.013 | 0.012 | 0.013 | 0.012 | 0.009 | 0.012 |
| ssa7552-038 (SAT) | 0.039 | 0.031 | 0.031 | 0.027 | 0.010 | 0.012 |
| **huck.col.11 (UNSAT)** | **7.502** | **7.071** | TIMEOUT | TIMEOUT | 32.64 | 16.51 |
| a_rphp045_05 | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| queen8_8.col.9 | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| factoring29986577x29986577 | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| sdiv15prop | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| Karatsuba7654321x1234567 | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| prime_119218851371 | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | UNSAT 47.72 |
| ortholatin-7 | SAT 58.30 | TIMEOUT | TIMEOUT | TIMEOUT | SAT 59.00 | SAT 16.93 |
| apn-sbox5-cut3-symmbreak | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |

Files solved of 26: `cryptominisat5` 20, Go `cdcl` 19, `minisat` 19, Rust `cdcl`
18, Rust `dfs` 17, Go `dfs` 16.

## Stage 53 to Stage 68 for `cdcl`

| File | Go: Stage 53 → now | Rust: Stage 53 → now | Go / `minisat` now | Rust / `minisat` now |
|---|---|---|---|---|
| uf250-0100 | 5.212 → 2.130 s | 4.955 → 1.979 s | 0.82x (was 1.93x) | 0.77x (was 1.83x) |
| uf250-03 | 4.827 → 2.110 s | 4.624 → 1.982 s | 4.8x (was 10.5x) | 4.5x (was 10.1x) |
| uuf250-0100 | 10.683 → 3.811 s | 10.242 → 3.704 s | 1.03x (was 2.77x) | 1.00x (was 2.66x) |
| huck.col.11 | 44.39 → 7.502 s | 40.62 → 7.071 s | 0.23x (was 1.31x) | 0.22x (was 1.20x) |

`minisat`'s own times are almost unchanged between the two sweeps (2.70 → 2.59 s,
3.86 → 3.72 s on the two 250-variable files), so the gain is `vibe_sat`'s. The
stages in between that plausibly account for it are 54 to 67; this report did not
separate them. I believe Stage 67's scheduled reduction is the main cause, given
that it roughly halved random-instance times on its own in `REPORT67.md`, but
I did not rerun with `-d` here to check.

## Observations

- **Where `vibe_sat` is still slower than `minisat`:** the small and mid-size
  instances by a constant factor (0.07 s against 0.017 s on `uf175-0100`, 0.04 s
  against 0.01 s on `ssa7552-038`), and `uf250-03` (about 4.5x). At the
  sub-10 ms level start-up cost dominates and the ranking is noise.
- **`dfs`** is far behind `cdcl` once instances need real learning: about 20x
  slower on `uf250-0100` (44 s in Go, 26 s in Rust), a timeout in Go on
  `uuf250-0100` (24 s in Rust), and it solves nothing in the industrial set.
  Rust `dfs` is faster than Go `dfs` on every random instance above 100
  variables, by 1.7 to 3x; the two `cdcl` implementations are within about 10%
  of each other.
- **`ortholatin-7`** is a coin flip for `vibe_sat` and `minisat`: Go `cdcl` solved
  it at 58.3 s, 1.7 s inside the cap, and Rust timed out; `minisat` finished at
  59.0 s. Only `cryptominisat5` solves it comfortably (16.9 s). `prime_119218851371`
  is again solved only by `cryptominisat5`, as in `REPORT53.md`. These are the
  algebraic instances the low-priority XOR/Gaussian-elimination item is about.
- The eight industrial files that everyone times out on, and `apn-sbox5-cut3-
  symmbreak`, are unchanged from `REPORT53.md`.

## Caveats

- One run per cell. The contaminated earlier sweep, which I do not quote, gave
  2.19 s and 4.19 s for Go `cdcl` on the two 250-variable files (against 2.13 s
  and 3.81 s here), so expect run-to-run differences of several percent or more,
  larger under load. Differences under about 10% should be read as ties. The
  borderline cases (`ortholatin-7`) can flip.
- Single-threaded only, 60 s cap, 26 files; the multi-threaded `cdcl` was not
  compared with `cryptominisat5`'s own multi-threaded mode.
- The file set is the old one, mostly small random instances; it says little
  about large industrial instances, where the nine we have are mostly out of
  reach for everyone at 60 s.

`todo.md` and `low_priority.md` were not touched.
