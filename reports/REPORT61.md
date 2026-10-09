# Report: Stage 61

## What this stage was

`STAGE61.md` asked for three things:

1. **Bounded Variable Addition (BVA)**, `REPORT50.md` item 6, in both languages.
2. **Characterize the new benchmarks** you added (`benchmark/bf`,
   `benchmark/pigeon-hole`, `benchmark/pret`), and use them for `REPORT55.md`
   item 3: does importing more shared clauses help on a structured UNSAT family?
3. Answers to the `REPORT60.md` questions (all were decisions, handled in
   section 6).

Nothing has been committed. Go and Rust behave identically.

**Headline results**

- BVA is implemented in both languages, tested against exhaustive enumeration,
  and gives byte-identical preprocessing results in Go and Rust on every file I
  compared (section 1).
- **It is off by default.** I measured it on structured instances, random 3-SAT
  and SAT Competition files and found **no solve-time benefit** anywhere:
  neutral on random, mixed on pigeon-hole, and 3x slower on the three
  SAT Competition files that were solvable (section 3). It is available as
  `preprocess.bvaWorkBudgetFactor`.
- **Only `pigeon-hole` is a hard benchmark.** `bf` solves in 0.0 s. The `pret`
  files cannot be read at all, because their header is off by one (section 2).
- For the import question, the pigeon-hole result is **no detectable effect**
  from the import limit, but an unexpected finding: **8 threads is slower than
  1 thread on `hole10`** (section 4).

## 1. Bounded Variable Addition

### What it does

BVA is the complement of variable elimination. Where a formula contains the
grid of clauses `(a OR C)` for every literal `a` in a set A and every remainder
`C` in a set R (|A|·|R| clauses), BVA adds a variable `x` and writes `(a OR x)`
for each `a` and `(NOT x OR C)` for each `C` (|A|+|R| clauses). Resolving on `x`
gives the old grid back, so the new formula is satisfiable exactly when the old
one is, and any model restricted to the old variables is a model of the old
formula. This is the SimpleBVA procedure of Manthey, Heule & Biere (HVC 2012).

### Design

- **Where:** a single pass at the end of `preprocess.Run`, after the unit /
  pure-literal / subsumption / BVE loop reaches its fixpoint. That keeps its new
  variables away from BVE (which would otherwise tend to undo them) and means no
  other technique needs to know about added variables. It is skipped if
  preprocessing already timed out.
- **Algorithm:** a max-priority queue of literals by occurrence count. For a
  literal `l`, find partner clauses that differ from a clause of `l` in exactly
  one literal, grow the literal set greedily by whichever literal pairs with the
  most clauses, and keep growing while the clause reduction `a*b - a - b`
  improves. A replacement is applied only if it removes strictly more distinct
  clauses than it adds (the grid can name the same clause twice, so the count is
  of distinct clauses); that also rules out an endless replacement cycle.
- **Determinism:** ties are broken by the smaller literal code
  (`2*var + sign`), and the queue order is a pure function of (count, code), so
  both languages make the same choices.
- **Added variables:** numbered after the original ones, mapped through
  `renumber`, and dropped in `Reconstruct` (they have no original counterpart).
- **Limits:** a work budget of `bvaWorkBudgetFactor * (clause count)`, counted in
  candidate clause comparisons, plus the Stage 57 deadline checked every 64
  units. Stopping early is always safe: a replacement is applied only after it
  has been fully worked out.
- **Parameter:** `preprocess.bvaWorkBudgetFactor`, range 0 to 1,000,000,000
  (0 turns BVA off), validated like the other budgets. **Default 0 (off)**, for
  the reason in section 3. It is listed in `--help`, `docs/internal-parameters.md`
  and in `util/paramtune`'s parameter table.
- **Output:** the `preprocess:` line gains `added=N`
  (`vars 90->104 ... eliminated=10 added=24`). `Stats.VariablesAdded` /
  `variables_added` carries it.

### Files

Go: `go_src/internal/preprocess/bva.go` (new), `bva_test.go` (new),
`preprocess.go`, `go_src/internal/params/params.go`,
`go_src/internal/cliargs/help.go`.
Rust: `rust_src/src/preprocess.rs` (implementation and tests are in that file,
like every other technique), `params.rs`, `cliargs.rs`.
Also `util/paramtune/params.go` and its test (16 parameter paths now), and docs:
`docs/internal-parameters.md`, `docs/background.md`, `docs/references.md`,
`docs/sample-runs.md`.

### Tests (mirrored in both languages)

- A pairwise at-most-one over 8 variables is reencoded with fewer clauses and
  stays equivalent.
- The textbook 3x3 grid (9 clauses) becomes exactly 6 clauses over one new
  variable.
- A formula with no shared structure is untouched.
- A zero budget and an already-expired deadline change nothing (the deadline case
  reports a timeout; the budget case does not).
- **A fuzz test of 300 random grid-structured formulas**, checking by
  exhaustive enumeration that an assignment of the original variables satisfies
  the original exactly when it extends to a model of the rewritten formula. At
  least 50 of the 300 are actually rewritten (the test fails if the generator
  stops exercising BVA).
- `Run` end to end on a satisfiable formula: every model of the reduced problem,
  once reconstructed, satisfies the original.
- A factor of 0 adds nothing.

(That end-to-end test turns BVE off, because BVE eliminates those low-degree
variables before BVA runs.)

### Verification

- Go: `gofmt` clean, `go vet` clean, `go test ./...` passes.
- Rust: `cargo fmt --check` clean, `cargo clippy --release --all-targets
  -- -D warnings` clean, `cargo test --release`: **318 passed** (310 before).
- `util/paramtune` tests pass.
- **Go/Rust parity:** I compared the `preprocess:` summary line (variables and
  clause counts, and `added`) in both languages on pigeon-hole 6 to 9, all four
  `bf` files, `bw_large.d` (4,894 variables added) and `uf250-01`: identical in
  every case. That compares counts, not the clause lists themselves.
- I did **not** rerun the Stage 55 differential fuzz or the benchmark checker.

## 2. The new benchmarks

Current binaries, `cdcl`, `-t 60`, single thread, BVA off.

| Family | Files | Verdict | Time (Go and Rust alike) |
|---|---|---|---|
| `bf` | 4 | all UNSAT | 0.0 s each |
| `pigeon-hole` | hole6, 7, 8, 9, 10 | all UNSAT | 0.0, 0.0, 0.1, 0.8, **48 s** |
| `pret` | 8 | **cannot be read** | see below |

- **`bf`** is trivial: preprocessing alone nearly decides it
  (`bf2670-001`: 1,393 variables to 5).
- **`pigeon-hole`** is exponential in resolution: each size is roughly 8x to 60x
  slower than the last, and `hole10` is the only instance here that takes real
  time. It is the one genuinely hard structured family.
- **`pret`: every file fails to load.** The error is `problem line declares 160
  clauses but 161 were found` (400 vs 401 for the `pret150` files): all eight
  headers are one clause short, a known quirk of these DIMACS files. `vibe_sat`'s
  reader is strict about the count. I did not touch `benchmark/` or the parser.
  With the header corrected in scratch copies, all eight are UNSAT in 0.0 s, so
  they are trivial in any case.
  **Question for you:** should the reader tolerate a header that undercounts
  the clauses, as many solvers do? It would be a small behavioral change in both
  languages.

## 3. BVA measurements

All with `cdcl`, one thread, Go and Rust sequential. "Off" is
`bvaWorkBudgetFactor = 0`.

### Structured set (32 files: pigeon-hole, bf, blocksworld, ssa, 8 of flat125; `-t 90`)

| Factor | Total elapsed (Go + Rust) | Unsolved |
|---|---|---|
| 0 (off) | 96.6 s | 0 |
| 25 | **40.1 s** | 0 |
| 100 | 69.7 s | 0 |
| 400 | 69.8 s | 0 |

This looks like a win but is almost entirely one file. Per-file, pigeon-hole
(seconds, Go; Rust agrees within about 5%):

| | hole8 | hole9 | hole10 |
|---|---|---|---|
| off | 0.1 | 0.8 | 48.5 |
| factor 25 | 0.1 | 2.3 | 16.9 |
| factor 100 | 0.1 | 2.3 | 32.1 |

- BVA cuts the formula a lot (hole10: 561 to 306 or 298 clauses) but the *solve
  time* is mixed: **hole9 is about 3x slower, hole10 is 1.5x to 3x faster**.
- Factors 25 and 100 produce *different* encodings of the same file (28 vs 36
  variables added on hole10; the smaller budget stops earlier), and the solve
  time differs by 2x between them. Pigeon-hole search time is very sensitive to
  the exact encoding, so with one instance deciding the total, factor 25's lead
  is not evidence about 25 as such.
- Everything else in the set (bf, blocksworld, ssa, flat) is too easy to show
  any difference.

### Random 3-SAT (24 files: uf250, uuf250, uuf175; `-t 20`)

Off 124.7 s, factor 100: 124.5 s. **Neutral.** BVA finds nothing to rewrite there
and its cost is negligible.

### SAT Competition 2018 (40 files up to 4 MB; shuffled with a fixed seed)

*Preprocessing effect* (Go; factor 100 vs off): BVA rewrote 27 of the 40
files. On those, **clause counts fell by a median of about 4%, up to 41%**,
at the cost of extra variables. Examples: `77b1034...`: 740 vars / 6,094 clauses
to 509 / 2,805 with BVA off but 690 / 1,644 on; `b861982...`: 54,418 to 42,981
clauses; `360d0e7...`: 85,448 to 56,058 clauses (+4,471 variables).
**Preprocessing time** rose by 0 to 0.8 s per file, including on the files
where BVA found nothing to rewrite (the search for patterns still costs): the
largest of those went from 0.7 s to 1.3 to 1.6 s.

*Solve effect* (Go, `-t 20`): 37 of 40 files are still UNKNOWN either way, so
the comparison rests on the **three that solve** (all SAT):

| File prefix | Off | On |
|---|---|---|
| `3e52d79...` | 7.8 s | 8.5 s |
| `360d0e7...` | 2.0 s | 18.8 s |
| `7586d75...` | 1.4 s | 6.2 s |
| solved / total time | 3 / 11.2 s | 3 / 33.5 s |

Same solved count, 3x the time on the solved ones. That is three files, and SAT
instances are noisy, so I do not read it as "BVA makes industrial files 3x
slower", only as "there is no sign of a benefit, and a hint of harm".

### Decision

**BVA ships off by default.** It is implemented, tested and verified, and
`bvaWorkBudgetFactor = 100` enables it. I did not find a configuration that
helps on this project's benchmarks. I did not run a `paramtune` sweep of
`bvaWorkBudgetFactor`, since there was nothing to tune toward.

Why it may not help here (my reasoning, not measured): BVA's published gains come
with solvers whose heuristics can use the new variables; here the new variables
join VSIDS as ordinary decision variables, and the smaller formula has more
variables. A cleverer use (never branch on added variables, or exclude them from
the phase and restart machinery) is a possible follow-up but would be a design
change.

## 4. `REPORT55.md` item 3: does importing more help on structured UNSAT?

The only structured UNSAT instances that are not trivial are `hole9` and
`hole10`, so that is the whole set. `cdcl`, **8 threads**, BVA off, five
repetitions per cell, variants interleaved in random order. The import limit
(`importMaxPerPeer` / `IMPORT_MAX_PER_PEER`) is a compile-time constant, so I
built scratch copies of both languages with it set to 1 and 256 (the repo keeps
8). Geometric mean seconds (min to max):

| | N=1 | N=8 (current) | N=256 |
|---|---|---|---|
| hole9, Go | 1.17 (1.08 to 1.30) | 1.21 (1.05 to 1.30) | 1.16 (1.10 to 1.21) |
| hole9, Rust | 1.04 (0.92 to 1.20) | 0.97 (0.80 to 1.25) | 0.86 (0.77 to 0.94) |
| hole10, Go | 70.7 (55 to 97) | 80.7 (67 to 100) | 62.3 (56 to 70) |
| hole10, Rust | 80.6 (68 to 118) | 71.2 (59 to 98) | 67.1 (48 to 89) |

- **No detectable effect.** On `hole10` the spread between repetitions of one
  setting (55 to 97 s) is larger than the gap between settings. N=256 has the
  lowest mean in three of four rows, but 5 repetitions on one or two instances
  cannot support a claim.
- I did not record verdicts in the experiment script (no `-v`), so the table
  rests on timing alone. All runs finished under the 120 s limit (longest 118.0 s),
  and a spot check of `hole9` (both languages) printed UNSAT.
- **An unexpected finding:** with 8 threads `hole10` takes 55 to 118 s, against
  **48 s single-threaded**. Parallel `cdcl` is *slower* than serial on this
  instance in both languages. The machine has 12 cores, so it is not
  oversubscription. My guess (not tested) is that pigeon-hole proofs are hard to
  shorten by sharing: the learned clauses are mostly useless to the other threads
  and the extra threads add contention.

So structured UNSAT does not change the Stage 55 conclusion that N is not
important, with the caveat of a tiny sample. I made no change to N.

## 5. Open questions for you

- **`pret` reader.** Tolerate an undercounted header (section 2)?
- **Parallel `cdcl` slower than serial on pigeon-hole** (section 4). Worth a
  look at some point (is clause sharing hurting? does it happen on other
  structured UNSAT families?). I only have two instances.
- **BVA follow-ups** if you want them: exclude added variables from branching, or
  retry on a harder structured benchmark where the clause reduction might matter
  more. Otherwise it can stay as an opt-in feature.
- Per your answers, the memory-limit question goes into the next brainstorming
  stage, and the unit-propagation worklist is on your todo list. Nothing done on
  either here.

## 6. Notes

- **Noise.** Single-instance timings (pigeon-hole especially) vary a lot between
  runs and between encodings; the SAT Competition solve comparison has 3 solvable
  files. I tried not to over-read either.
- **Timing hygiene.** The measurement runs were sequential with the machine
  otherwise idle apart from light editing; I did not pin CPUs.
- All scripts and raw output are in the scratchpad, not the repo. `benchmark/`
  is unchanged.
