# Report: Stage 25

## Summary

Stage 25 took a hard look at preprocessor performance, per
`REPORT22.md` item 2 and `REPORT24.md`'s open Rust-vs-Go finding.
Headline results:

1. **Profiling gave a clear, decisive answer to "which technique is
   the cost," and it overturns `REPORT22.md`'s own guess.** Real
   per-technique timing on the two flagship instances `REPORT21.md`
   named shows **subsumption elimination, not bounded variable
   elimination (BVE), dominates preprocessing time** on real,
   clause-count-heavy instances (87-94% of total preprocessing time on
   `bw_large.c.cnf`/`.d.cnf`). BVE does still dominate on a different
   kind of instance (`ssa7552-158.cnf`, which eliminates almost every
   variable via cascading BVE), so both matter, but subsumption is the
   right first target and the one this stage's multithreading work
   targets.
2. **`REPORT24.md`'s "Rust preprocessing is slower than Go" finding
   is now root-caused and fixed.** `perf` profiling found ~48% of
   total Rust preprocessing time was going into `SipHash` hashing and
   `HashMap`/`HashSet` allocation, in three call sites that only ever
   operate on very short clauses -- a plain slice scan is faster than
   hashing at this size, in both languages. Fixing this closes almost
   all of the previously-reported gap (Rust: ~22.4s -> ~7.85s on
   `bw_large.c.cnf`'s internal preprocessing time; Go, which had the
   same anti-pattern with a cheaper hash function, improved too:
   ~12.6s -> ~7.3s).
3. **`--num-threads > 1` now speeds up preprocessing** (subsumption
   elimination specifically), fully deterministically -- the result is
   always byte-for-byte identical to the single-threaded one, only
   faster. Measured on `bw_large.d.cnf`: Go 47.5s -> 25.4s at 8
   threads (1.87x); Rust 53.4s -> 23.0s at 8 threads (2.32x).

No command line parameters changed, per `STAGE25.md`; `--num-threads`
already existed and preprocessing is simply a new consumer of it, like
`hc`/`ws`/`dfs`/`cdcl` each became in Stages 17/18/21.

## Profiling methodology, and a real pitfall it hit

**Go**: `runtime/pprof` via the Stage 24 `bench_test.go` benchmarks,
plus (for finer-grained "which technique" attribution) temporary
`time.Now()`/`time.Since()` instrumentation added directly around each
of the four technique calls inside `Run`, measured, and reverted --
the same add/measure/revert pattern `REPORT23.md` established.

**Rust**: `perf record`/`perf report` -- usable for the first time in
this project, now that the target machine (an Ubuntu laptop, not the
previous WSL2 environment) has `kernel.perf_event_paranoid=1` set, per
`STAGE24.md`'s and this stage's own setup notes. The same temporary
per-technique `Instant` timing pattern was also used for the "which
technique" breakdown, mirroring Go's.

**A real methodology pitfall worth recording**: this machine's CPU is
a hybrid P-core/E-core design (a 13th Gen Intel mobile part). The
first `perf record` run used the default hardware `cycles` event,
and `perf report` silently attributed almost all samples to only
`cpu_atom/cycles/P` (31 samples) despite `perf record` itself
reporting ~90,000 samples captured -- the rest were on the other core
type's PMU and invisible to a report that doesn't ask for both.
Switching to a software event (`-e task-clock:u`, not tied to either
core type's specific performance-monitoring unit) fixed this and
produced a report with the full ~92,000 samples properly attributed.
Flagging this since it would silently produce a misleading profile
(mostly noise) with no error message, on any hybrid-core Intel
machine, in any project.

## Which technique is the cost: subsumption, usually -- BVE, sometimes

Per-technique cumulative time inside `Run`, before any fix, on the
instances `REPORT21.md` and this stage named:

| File | vars/clauses | unit | pure | **subsumption** | BVE |
|---|---|---|---|---|---|
| `bw_large.c.cnf` | 3016/50457 | 3.3ms | 0.4ms | **10.83s (87%)** | 1.68s |
| `bw_large.d.cnf` | 6325/131973 | 7.9ms | 0.5ms | **101.8s (94%)** | 5.87s |
| `ssa7552-158.cnf` | 1363/3034 | 2.7ms | 0.4ms | 22.5ms (0.7%) | **3.34s (99%)** |

The pattern: subsumption's `O(clauses^2)` pairwise comparison
dominates on instances with a *large clause count* relative to how
much actually gets eliminated (the two `bw_large` files: only 14-18
variables ever get eliminated by BVE, but every one of 50,457+/131,973+
clauses gets compared against every other one, repeatedly, across
preprocessing's fixpoint rounds). BVE dominates instead on an instance
where cascading elimination consumes almost the entire formula
(`ssa7552-158.cnf`: 1132 of 1363 variables eliminated -- BVE's
`resolve()` gets called a very large number of times as a direct
consequence). `REPORT22.md`'s guess ("my guess is BVE... but it's a
guess") turns out right for the second kind of instance and wrong, by
roughly two orders of magnitude, for the first -- exactly the outcome
`STAGE25.md` was checking for by asking to profile before
parallelizing.

## Single-threaded fix: stop hashing tiny clauses

All three "does clause X contain literal L" hot spots in both
languages' preprocessors used a hash-based set (`map[cnf.Literal]bool`
in Go; `HashSet<Literal>` in Rust) -- in `eliminateSubsumedClauses`'s
`O(clauses^2)` inner loop, in BVE's `resolve()` (called
`pos-occurrences x neg-occurrences` times per candidate variable), and
in BVE's own clause-index removal set. This project's clauses are
almost always short (a handful of literals; a few dozen at most for
structured instances), and a hash lookup's own overhead -- computing
the hash, probing a bucket -- costs more than just scanning that many
literals directly. This is a much sharper effect in Rust, where the
default hasher (`SipHash`) is chosen for DoS-resistance against
adversarial input, not speed, than in Go, whose map hash is cheaper
per lookup but still not free.

**Confirmed with `perf`, not assumed**: profiling
`eliminate_subsumed_clauses` (Rust) before this fix found
`RandomState::hash_one` alone consumed ~44% of total preprocessing
time on `bw_large.c.cnf`, plus another ~4% in `HashMap` insert/rehash
-- essentially half of all preprocessing time spent hashing single
literals.

**The fix**, applied identically in both languages: replace each
`HashSet`/`map` with a plain slice/`Vec` and a linear scan
(`slices.Contains` in Go, `[Literal]::contains` in Rust) -- no
hashing, no allocation, better cache locality for data this small.
This is a pure, behavior-preserving simplification (same asymptotic
complexity per call; smaller constant factor), verified via all
existing unit tests passing unchanged and confirmed with fresh
before/after timing:

| File | Technique | Go before | Go after | Rust before* | Rust after |
|---|---|---|---|---|---|
| `bw_large.c.cnf` | subsumption | 10.83s | 6.18s | ~11s (implied, 48% of ~22.8s total) | 7.17s |
| `bw_large.c.cnf` | BVE | 1.68s | 1.09s | (not isolated) | 0.67s |
| `bw_large.c.cnf` | **total** (internal `Run` time) | 12.63s | 7.27s | ~22.4-23.1s | 7.85s |
| `ssa7552-158.cnf` | subsumption | 22.5ms | 14.2ms | (negligible either way) | 11.8ms |
| `ssa7552-158.cnf` | BVE | 3.34s | 2.12s | (not isolated before) | **1.09s (3.1x)** |

\* Rust's per-technique breakdown didn't exist until this stage added
it (alongside the fix), so most "before" cells are the one
whole-run number `REPORT24.md`/this stage's own `perf` session
measured, not a per-technique split.

This closes almost the entire gap `REPORT24.md` reported: Rust's
`bw_large.c.cnf` preprocessing went from ~1.8x *slower* than Go's to
~8% slower -- roughly the same, ordinary, close-to-parity gap seen
elsewhere in this project, not the reversed, disproportionate one
`REPORT24.md` flagged. The `resolve()` fix alone gave Rust a 3.1x win
on the BVE-dominated `ssa7552-158.cnf` case, versus a smaller (but
still real) ~1.6x win in Go on the same file -- consistent with
`SipHash` (Rust's default) being disproportionately expensive relative
to Go's own map hash for this exact shape of workload (many, very
small, short-lived hash sets).

## Multithreaded preprocessing: subsumption elimination

`STAGE25.md` asks for `--num-threads > 1` to speed up preprocessing.
Given the profiling above, subsumption elimination -- the technique
that actually dominates on the instances motivating this stage -- is
the target, not BVE; see "Why BVE was not also threaded" below for the
reasoning on the other technique.

### Design: a provably-equivalent batch parallelization

`eliminateSubsumedClauses`'s sequential form has an outer-loop
optimization, `if !keep[i] { continue }` (skip using an
already-eliminated clause as a subsumer), that is ordering-dependent
and therefore not naively safe to split across threads. The key
insight that makes a safe, exactly-equivalent parallel version
possible: **that skip is provably redundant for correctness, only ever
saving redundant work.** If clause `i` is itself later found to be
subsumed by some clause `i2` (`i2 ⊆ i`), then by transitivity of the
subset relation, `i2 ⊆ i ⊆ j` implies `i2 ⊆ j` directly -- so whatever
`j`'s `i` would go on to mark non-keep, `i2` marks too, independently,
via its own full pass over every `j` (which either already ran, if
`i2`'s index is smaller, or will still run before anyone marks `i2`
itself non-keep, if `i2`'s index is larger -- either way `i2`'s own
contribution is never lost). So evaluating every ordered pair `(i, j)`
against the fixed input snapshot, regardless of any other pair's
outcome, yields the exact same final "keep" set as the sequential,
short-circuiting version.

That is exactly what `eliminateSubsumedClausesParallel`/
`eliminate_subsumed_clauses_parallel` does: partitions the outer `i`
range across `--num-threads` workers (goroutines in Go,
`std::thread::scope` in Rust, matching this project's established
per-language convention), each scanning the full, read-only clause
snapshot; `keep` becomes an atomic bool array (`[]atomic.Bool` /
`Vec<AtomicBool>`) so concurrent writes -- always the same value,
`false`, since `keep` only ever moves one direction -- are safe
without a lock. `--num-threads <= 1` delegates straight to the
original, untouched sequential function, matching every other
algorithm's own `--num-threads` convention since Stage 17.

Verified this equivalence holds in *code*, not just in the proof
above: `TestEliminateSubsumedClausesParallelMatchesSequentialChained`/
`test_eliminate_subsumed_clauses_parallel_matches_sequential_chained`
constructs the exact `i2 ⊆ i ⊆ j` chain the proof depends on and
checks the parallel version (at 1/2/3/4/8 threads) removes the same
two clauses the sequential version does;
`...MatchesSequentialOnRealFile` repeats the check against a real
benchmark file (`blocksworld/anomaly.cnf`) at 1/2/3/4/8/16 threads;
`TestRunProducesIdenticalResultsRegardlessOfNumThreads`/
`test_run_produces_identical_results_regardless_of_num_threads` checks
the *whole* `Run`/`run` pipeline (not just subsumption in isolation)
produces byte-for-byte identical `Stats` and simplified-problem output
across 1/2/4/8/16 threads. Preprocessing was already the only
fully-deterministic (no RNG) part of this project; this stage keeps
that property even multithreaded -- unlike `hc`/`ws`/`dfs`/`cdcl`'s own
`--num-threads` support, there is no "which worker wins" race here at
all, since no clause is ever discarded based on which thread got
there first (every discard is a fixed fact about the input, not a
race outcome).

### Why BVE was not also threaded

`ssa7552-158.cnf` shows BVE can dominate too, and the single-threaded
fix alone gave it a large win there (3.1x in Rust) -- but I did not
extend `--num-threads` to it this stage, for a concrete reason found
while designing it, not just time pressure: `eliminateVariables`/
`eliminate_variables` eliminates **one variable at a time**, fully
rebuilding its occurrence lists and restarting after each one (see its
own doc comment: "occurrence lists above are now stale; rebuild and
retry"). For `ssa7552-158.cnf` alone that is 1132 separate
eliminations. The natural place to parallelize BVE's own expensive
step (`resolve()`, called `pos-occurrences x neg-occurrences` times
per candidate variable) would mean spawning a fresh batch of
`--num-threads` workers *for every one of those 1132 rounds* -- and in
Rust particularly, `std::thread::spawn`/`scope` is a real OS thread
per call, not a cheap green thread; 1132 rounds x a handful of threads
each is enough spawn/join overhead that it could plausibly cost more
than the constant-factor win the single-threaded fix already
delivered, especially for the (common) case of a low-degree variable
whose own `resolve()` batch is tiny. Getting a real win here would need
a different design -- batching multiple independent (non-clause-
overlapping) eliminations into one pass with one shared thread-pool
dispatch, or a persistent worker pool reused across rounds instead of
spawned per round -- which is a bigger, riskier change than this
stage's single, well-scoped subsumption win, and better suited to a
dedicated future stage if BVE-dominated instances turn out to matter
enough to justify it.

### Benchmark: does it actually help?

Measured with `--algorithm=hc --alg-params=1` rather than `dfs`,
deliberately: an early attempt using `dfs` showed *no* wall-clock
improvement at all from threading (even a regression at high thread
counts) -- which turned out to be `dfs`'s own parallel search phase
(already documented in `REPORT18.md`/`REPORT19.md` as suffering
Go-deque-lock contention that gets worse with more threads), not a
problem with this stage's preprocessing work at all. Confirmed by
timing preprocessing's own internal duration separately from total
wall-clock time: preprocessing itself scaled correctly under `dfs`
too (Go, `bw_large.c.cnf`: 7.0s -> 4.1s from 1 to 8 threads) even
while total wall-clock time went the wrong way because of the
*unrelated*, already-known `dfs` issue downstream. `hc` with a single
restart does negligible algorithm-side work, so its total wall-clock
time is essentially preprocessing's own time -- a clean measurement.

`bw_large.d.cnf` (the larger, harder flagship instance), wall-clock
time by `--num-threads`:

| threads | Go | Rust |
|---|---|---|
| 1 | 47.5s | 53.4s |
| 4 | 30.1s | 26.8s |
| 8 | 25.4s | 23.0s |

Both languages show real, monotonic speedup (Go: 1.87x at 8 threads;
Rust: 2.32x). Rust scales somewhat better, consistent with this
project's established pattern (`REPORT18.md`/`REPORT19.md`: Go's
hand-rolled, mutex-guarded structures tend to show more contention at
higher thread counts than Rust's atomic/lock-free equivalents, even
though both use the same atomic-bool design here) -- and consistent
with Amdahl's law, since BVE (~0.67-1.09s, unparallelized) becomes a
larger fraction of an ever-shrinking total as thread count rises.
`bw_large.c.cnf` (smaller) showed the same shape: Go 7.5s -> 4.0s
(1.87x); Rust ~10.3-10.6s -> 5.75s (~1.8-1.85x).

## Verification

- **Unit tests**: Go's `preprocess` package grew from 13 to 16 tests;
  Rust's grew from 17 to 20. New tests per language: the
  chained-subsumption equivalence test, the real-file equivalence
  test (both across several thread counts including more threads than
  the file has cores' worth of obvious parallelism), and the
  whole-`Run`-is-deterministic test described above. `go build ./...`,
  `go vet ./...`, `gofmt -l .`, `go test ./...` (9 packages, 196 tests
  total) are all clean; `cargo fmt --check`, `cargo clippy
  --all-targets -- -D warnings`, `cargo test --release` (193 tests)
  are all clean.
- **Race detector**: `go test -race -count=5 ./internal/preprocess/...`
  is clean. Rust has no runtime equivalent, but no `unsafe` is used
  anywhere in the new code, so its ownership/`Send`/`Sync` rules rule
  out data races at compile time (the parallel function would not
  compile with a plain, non-atomic `Vec<bool>` shared across the
  `thread::scope` closures).
- **Cross-language correctness sweeps** (via `util/benchcompare`,
  Stage 24's harness): `cdcl` at `--num-threads=4` across 40 sampled
  files from `uf100-430`/`uuf100-430`/`uf175-753`/`uuf175-753` --
  0 mismatches, 0 independent-verification failures, both languages
  agreeing on 20 SAT + 20 UNSAT. `dfs` at `--num-threads=4` across
  **every file** in `blocksworld`/`ssa`/`flat125-301`/`flat200-479`
  (215 files, the exact directories this stage's preprocessing changes
  touch most) -- 213/215 solved with 0 mismatches and 0 verification
  failures in both languages; the remaining 2 (`bw_large.d.cnf`, both
  languages) hit the harness's own `--hard-timeout-secs` safety net,
  not a correctness problem -- that file's combined
  preprocessing-plus-search time at this stage's settings simply
  exceeded the sweep's time budget, consistent with the numbers above.

## Command line arguments

None changed. `--num-threads`/`-z` already existed (Stage 17); this
stage only makes preprocessing a new consumer of it, exactly as
`dfs`/`cdcl` themselves became new consumers of the same flag in
Stages 18/21. Help text in both languages was updated to describe
preprocessing's threading behavior, per the established convention of
updating `--num-threads`'s help entry whenever a new part of the
program starts honoring it.

## Testing

- Go: `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./...`
  (9 packages, 196 tests), `go test -race -count=5
  ./internal/preprocess/...` all clean.
- Rust: `cargo build --release`, `cargo build --release --benches`,
  `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`,
  `cargo test --release` (193 tests) all clean.
- Manual/scripted cross-language and performance verification as
  described above.

## Open questions / notes for you

- **BVE is not yet threaded** -- see "Why BVE was not also threaded"
  above for the concrete design obstacle (many small, per-round
  thread-spawn batches) rather than just scope-cutting. If
  BVE-dominated instances like `ssa7552-158.cnf` turn out to matter a
  lot to you, a future stage could pursue a persistent-worker-pool or
  batched-independent-elimination redesign -- but the single-threaded
  hashing fix alone already gave that specific file a 3.1x win in Rust
  (1.6x in Go), which may already be enough.
- **The hybrid-CPU `perf` pitfall** (default `cycles` event silently
  sampling only one core type) is worth remembering for any future
  profiling session on this same machine -- always prefer a software
  event (`task-clock`, `cpu-clock`) or explicitly request both
  `cpu_core` and `cpu_atom` PMUs.
- Preprocessing's threading only ever speeds up subsumption
  elimination; unit propagation and pure literal elimination are
  already fast enough (sub-millisecond to low-millisecond even on the
  largest instances measured) that threading them would not be
  worthwhile.
- No language/toolchain version changes needed.
