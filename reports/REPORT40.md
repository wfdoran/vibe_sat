# Report: Stage 40

## Summary

A time comparison of `vibe_sat` (Go and Rust, `--algorithm=cdcl`)
against `minisat` (2.2.1) and `cryptominisat5` (5.8.0), all single-
threaded, all default solving settings, across 25 problems chosen for
a mix of random vs. industrial and SAT vs. UNSAT. No code changes
this stage — this is a measurement/reporting exercise only.

**Headline finding**: on the easy end of the set (16 of 25 problems —
every random 3-SAT instance up to 175 variables, all of `blocksworld`,
and both `ssa` circuit-fault files), all four solvers finish in well
under a second and the differences don't matter practically. A real
gap opens up on harder instances: at 250-variable random 3-SAT,
`minisat`/`cryptominisat5` are 3-10x faster than either `vibe_sat`;
on the 9 real industrial instances sampled from SAT Competition 2018,
`minisat` and `cryptominisat5` each solved instances within 60 seconds
that both `vibe_sat` implementations could not, though 6 of those 9
instances (including the two competition-grade solvers) also timed
out — meaning several of them are simply hard at this time budget for
everyone, not a `vibe_sat`-specific weakness. Go and Rust `vibe_sat`
track each other closely throughout, as in every prior stage.

## Method

### Problem selection (25 files)

**Random 3-SAT (SATLIB, 11 files)** — one instance from each size
class already in `benchmark/`, giving known ground truth (`uf*` is
satisfiable by construction, `uuf*` unsatisfiable):
`uf20-01`, `uf50-01000`, `uf75-0100`, `uf100-01000`, `uf175-0100`,
`uf250-0100` (SAT); `uuf50-01000`, `uuf75-0100`, `uuf100-01000`,
`uuf175-0100`, `uuf250-0100` (UNSAT).

**Structured, already-committed (5 files)** — planning and circuit-
fault instances: `blocksworld/anomaly.cnf`, `blocksworld/medium.cnf`,
`blocksworld/bw_large.a.cnf`, `ssa/ssa0432-003.cnf`,
`ssa/ssa7552-038.cnf`.

**Industrial (9 files, from the local `sat_comp/2018` SAT Competition
corpus)**, picked for family diversity, moderate size (72 KB-1.5 MB,
so a run finishes or times out in a reasonable window) and unknown
verdict a priori: `huck.col.11` (graph coloring), `a_rphp045_05`
(relaxed pigeonhole), `queen8_8.col.9` (graph coloring),
`factoring29986577x29986577`, `sdiv15prop` (division circuit),
`Karatsuba7654321x1234567` (multiplication circuit),
`prime_119218851371`, `ortholatin-7` (orthogonal Latin square), and
`apn-sbox5-cut3-symmbreak` — the one file `REPORT31.md`/`REPORT39.md`
already found `vibe_sat`'s `cdcl` can't solve at any tested work
budget, included deliberately to see whether mature, competition-grade
solvers fare any differently on it within the same time budget.

### Solvers and settings

- `vibe_sat` (Go and Rust): built in optimized/release mode,
  `--algorithm=cdcl --num-threads=1` (single-threaded is `vibe_sat`'s
  own default; passed explicitly for clarity).
- `minisat` 2.2.1 (Ubuntu package `1:2.2.1-5build2`): default
  settings, no flags beyond the required input/output file arguments.
  Inherently single-threaded — has no thread option.
- `cryptominisat5` 5.8.0 (Ubuntu package `5.8.0+dfsg1-2`): default
  settings, `-t 1` passed explicitly (its own default is already 1
  thread) and `--verb 0` to suppress its otherwise very verbose
  banner/statistics output (a display-only flag, not a solving-
  behavior one).
- A 60-second wall-clock cap per solver per problem (`timeout 60`),
  chosen to keep the full 25 x 4 sweep's total run time bounded while
  still giving every solver a real chance on the easier 16 of 25
  problems; a run that hits this cap is reported as `TIMEOUT`, not
  guessed at.
- Every run sequential, one solver/file at a time — never two solvers
  racing on the same machine at once, so wall-clock time reflects each
  run's own cost, not contention with a concurrent run (the same
  discipline `util/benchcompare` already follows for the Go-vs-Rust
  comparison).

### A DIMACS parsing wrinkle (not a `vibe_sat` issue)

Both `minisat` and `cryptominisat5` rejected all 11 SATLIB-format
random 3-SAT files outright with a parse error on the trailing `%`
and lone `0` lines SATLIB appends after the last clause (a decades-old
convention some tools still emit) — `vibe_sat`'s own parser already
tolerates this construct (it's a legacy no-op, not part of the actual
DIMACS clause data), and neither `minisat` nor `cryptominisat5` does.
A cleaned copy (that trailer stripped) was used as those two solvers'
input for exactly the 11 affected files; `vibe_sat` ran against the
original, unmodified files. Separately, `cryptominisat5` also rejected
both `ssa*` files over tab-delimited literal tokens (`435\t0`, a valid
DIMACS whitespace convention `minisat` and `vibe_sat` both already
accept) — the same cleaned copies had tabs normalized to spaces before
being handed to `cryptominisat5`. Every one of these 13 fixes is a
pure whitespace/formatting normalization with no effect on the
instance's actual clauses; this was confirmed by checking that every
solver's SAT/UNSAT verdict on the cleaned files matches every other
solver's verdict on the original files (see "Verification" below).

## Results

Wall-clock seconds (4 significant figures), `TIMEOUT` = hit the
60-second cap. Every verdict below was cross-checked: wherever two or
more solvers reached a definite verdict on the same file, they agreed
— no disagreement was found anywhere in this sweep.

| File | go (cdcl) | rust (cdcl) | minisat | cryptominisat5 |
|---|---|---|---|---|
| uf20-01.cnf (SAT) | 0.007s | 0.006s | 0.005s | 0.009s |
| uf50-01000.cnf (SAT) | 0.006s | 0.004s | 0.005s | 0.008s |
| uf75-0100.cnf (SAT) | 0.013s | 0.011s | 0.011s | 0.013s |
| uf100-01000.cnf (SAT) | 0.009s | 0.007s | 0.008s | 0.015s |
| uf175-0100.cnf (SAT) | 0.081s | 0.076s | 0.014s | 0.037s |
| uf250-0100.cnf (SAT) | 8.283s | 7.358s | 2.640s | 3.208s |
| uuf50-01000.cnf (UNSAT) | 0.004s | 0.005s | 0.006s | 0.011s |
| uuf75-0100.cnf (UNSAT) | 0.009s | 0.008s | 0.008s | 0.012s |
| uuf100-01000.cnf (UNSAT) | 0.016s | 0.013s | 0.010s | 0.016s |
| uuf175-0100.cnf (UNSAT) | 0.106s | 0.089s | 0.026s | 0.075s |
| uuf250-0100.cnf (UNSAT) | 33.99s | 30.48s | 3.917s | 9.572s |
| anomaly.cnf (SAT) | 0.005s | 0.005s | 0.006s | 0.011s |
| medium.cnf (SAT) | 0.011s | 0.009s | 0.009s | 0.011s |
| bw_large.a.cnf (SAT) | 0.022s | 0.020s | 0.012s | 0.013s |
| ssa0432-003.cnf (UNSAT) | 0.033s | 0.024s | 0.009s | 0.014s |
| ssa7552-038.cnf (SAT) | 0.381s | 0.238s | 0.008s | 0.013s |
| huck.col.11.cnf | TIMEOUT | TIMEOUT | UNSAT 35.24s | UNSAT 18.32s |
| a_rphp045_05.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| queen8_8.col.9.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| factoring29986577x29986577.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| sdiv15prop.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| Karatsuba7654321x1234567.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| prime_119218851371.cnf | TIMEOUT | TIMEOUT | TIMEOUT | UNSAT 53.00s |
| ortholatin-7.cnf | TIMEOUT | TIMEOUT | TIMEOUT | SAT 17.42s |
| apn-sbox5-cut3-symmbreak.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |

## Observations

- **On 16 of 25 problems — all random 3-SAT up to 175 variables, and
  every structured/planning instance — the four solvers are within a
  fraction of a second of each other**, and the ranking among them
  isn't consistent from file to file. At this scale the difference is
  noise, not a real capability gap.
- **A real gap opens at 250-variable random 3-SAT.** `minisat` is
  fastest on both the SAT and UNSAT instance (2.64s / 3.92s);
  `cryptominisat5` close behind (3.21s / 9.57s); `vibe_sat`
  (Go and Rust) 2.3-9x slower than `minisat` (7.4-34.0s). This is the
  clearest apples-to-apples "harder instance, same family" comparison
  in the set, and it's not close.
- **On real industrial instances, the two mature solvers show a clear
  edge, but the instances are also just genuinely hard.** Only one of
  the 9 (`huck.col.11`, graph coloring) was solved by both `minisat`
  and `cryptominisat5` within 60 seconds while both `vibe_sat`
  implementations timed out — a case where decades of additional
  solver engineering (more sophisticated preprocessing/inprocessing,
  restart and phase heuristics tuned against huge competition
  benchmark sets) visibly pays off. Two more (`prime_119218851371`,
  `ortholatin-7`) were solved *only* by `cryptominisat5`, not
  `minisat` either — consistent with `cryptominisat5`'s much heavier
  built-in inprocessing (Gaussian elimination, XOR extraction, and
  more) mattering specifically on these algebraically-structured
  instances. The remaining 6 of 9 (including the two graph-coloring/
  pigeonhole/circuit families and `apn-sbox5-cut3-symmbreak`) timed
  out for *all four* solvers — `minisat` and `cryptominisat5` included
  — at this time budget, so those specific instances say more about
  60 seconds not being enough time for anyone than about `vibe_sat`
  specifically.
- **Go and Rust `vibe_sat` track each other closely everywhere**, Rust
  consistently a little ahead of Go on the handful of measurably-slow
  cases (7.36s vs. 8.28s at `uf250`; 30.48s vs. 33.99s at `uuf250`) —
  unsurprising and consistent with every prior stage's cross-language
  measurements; not a new finding.
- **`apn-sbox5-cut3-symmbreak.cnf` remains unsolved by every solver
  tested here within 60 seconds**, `minisat` and `cryptominisat5`
  included. This doesn't overturn `REPORT31.md`/`REPORT39.md`'s
  findings about this file — if anything it's a useful data point that
  this specific instance is hard in an absolute sense, not just
  relative to `vibe_sat`'s own preprocessing/search choices.

## Verification

- Every one of the 25 cleaned/original files was parse-checked against
  both `minisat` and `cryptominisat5` (a fast, `timeout 3` pass just
  looking for a `PARSE ERROR`) before running the real timed sweep, so
  the two parsing fixes above (SATLIB trailer, tab-delimited literals)
  were caught and corrected up front rather than discovered mid-run.
- Every verdict in the results table was cross-checked against every
  other solver's verdict on the same file: no disagreement anywhere.
  This is the same "measurement, not assumption" standard the project
  applies to internal Go-vs-Rust comparisons, extended here to two
  external, independently-implemented solvers as a third and fourth
  witness.
- Both `vibe_sat` binaries were built in optimized/release mode
  (`go build` for Go; `cargo build --release` for Rust) — the same
  build configuration `util/benchcompare` and every prior timing-
  sensitive stage has used, not a debug build.
- All runs were sequential and single-threaded, one solver/file
  combination running at a time on an otherwise idle machine, to keep
  wall-clock time meaningful.

## Documentation

No documentation changes this stage — a one-off external comparison
doesn't add a new CLI flag, algorithm, or technique to either
`go_src`/`rust_src`, so nothing in `docs/` needs updating.

Scratch files (the 25-file working copy, cleaned trailer-stripped
copies, the comparison script, raw timing output) lived entirely
outside the repository and were not committed.

## Questions for you

- `minisat`'s and `cryptominisat5`'s edge shows up most clearly on
  exactly the two kinds of instance this project's own
  `docs/background.md` "Go versus Rust" section already flags as
  where a from-scratch solver's youth would show: larger random
  instances (where raw core-loop throughput and years of low-level
  tuning compound) and industrial ones (where preprocessing/
  inprocessing sophistication matters most). Given `STAGE39.md`'s
  auto-tuning harness now exists, is a focused tuning pass against
  `minisat` specifically on the 250-variable random set worth a future
  stage, or is closing that gap not a goal of this project (which has
  mostly framed itself as "build and understand a real solver," not
  "match production solver performance")?
- Want the 60-second cap raised for a follow-up run specifically on
  the 6 instances every solver timed out on, to see whether any of
  them are solvable by anyone given more time, or is 60 seconds a
  reasonable enough budget to call this comparison done?
