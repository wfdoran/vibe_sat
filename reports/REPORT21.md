# Report: Stage 21

## Summary

Stage 21 is complete for both the Go and Rust versions of `vibe_sat`:
`--algorithm=cdcl` is now multithreaded, implementing Option B of
`reports/REPORT20.md` (a continuously shared clause pool; every
worker independently runs the ordinary single-threaded search over
the *complete* original problem, no divide-and-conquer). `cdcl` reuses
the `--num-threads`/`-z` flag introduced in Stage 17 unchanged. A new
fourth restart-strategy value, `--alg-params` val2=4 (round-robin
across quadratic/geometric/Luby by worker index), is added, and
becomes the default the moment `--num-threads > 1` unless you
explicitly ask for something else -- exactly as specified.

Because every worker in Option B searches the whole problem
independently, this design turned out to be structurally much closer
to Stage 17's `hc`/`ws` (embarrassingly parallel, CAS-based single
winner) than to Stage 18's `dfs` (BFS-seeded, work-stealing,
termination detection): there is no work to redistribute and no
partial-coverage termination question, so no analogue of Stage 18's
`Terminator` was needed at all. The only genuinely new mechanism is
continuous clause sharing, and the lock-free ring-buffer design
validated in `util/clausesharing/` last stage ported over with no
surprises.

One real finding from benchmarking, worth flagging up front since it
shapes how to read the numbers below: **on two of the four new
Stage-21 benchmark sets (`blocksworld`'s harder instances, and the
harder `ssa` instances), essentially all wall-clock time turned out to
be spent in single-threaded Stage 8 preprocessing, not in the
(correctly, genuinely parallel) `cdcl` search this stage threads.**
That was surprising enough that I tracked it down with actual
instrumentation before writing this up (see the Benchmark section) --
it isn't a bug in this stage's work, but it means those two benchmark
sets aren't good evidence either way for whether the threading here
works, and a properly search-bound instance (`uuf250-1065`) was needed
to get a real read on it.

## Restart strategy

Implemented exactly as specified. `RestartRoundRobin`
(`cdcl.RestartRoundRobin` / `RestartStrategy::RoundRobin`, value 4) is
a *meta*-strategy: no `solver`/`run_loop` call ever actually holds it
as its own `restartStrategy` -- `resolveRestartStrategy`/
`resolve_restart_strategy` resolves it to
`{Polynomial, Geometric, Luby}[threadIndex % 3]` before any solver is
constructed, so `restartThreshold`/`restart_threshold` never needs a
case for it at all. Assigning by spawn-order index turned out not to
be "a little tricky with goroutines" the way `STAGE21.md` was
prepared for -- `RunParallel` already hands every worker goroutine an
explicit `i` at spawn time (the same index Stage 17/18 already use for
the winner slot and per-worker RNG derivation), so a deterministic,
exactly-even round-robin was no harder than the random fallback would
have been, in either language.

`resolveRestartStrategy` is called with thread index 0 for the
single-threaded `Run`/`run` too, not just `RunParallel`'s workers --
so `--num-threads=1 --alg-params 2 4` (explicitly asking for
round-robin with one thread) resolves to the same `RestartPolynomial`
worker 0 of a real round-robin run would get, rather than being
silently mishandled.

Default selection (in `main.go`'s `runCDCL`/`main.rs`'s `run_cdcl`,
not in the `cdcl` package itself, since only the CLI layer knows
whether the user explicitly passed val2 or is relying on the
default):

```
restartStrategy := RestartPolynomial              // unchanged single-threaded default
if val2 explicitly given:
    restartStrategy = that value                   // always wins, even 0
else if --num-threads > 1:
    restartStrategy = RestartRoundRobin             // new multithreaded default
```

Verified directly: `-v 1`'s `cdcl: ...` banner shows `restart=2` at
`--num-threads=1` (default unchanged), `restart=4` at
`--num-threads=4` with no `--alg-params` (new default engaged), and
`restart=0` at `--num-threads=4 --alg-params 2 0` (explicit choice
wins over the threaded default) -- identical text from both binaries.

## Design

### No termination-detection protocol needed

STAGE20.md's Option B means every thread searches the complete
original problem, so any single worker's own verdict -- SAT *or*
UNSAT -- is already the authoritative answer for the whole run the
moment it's reached. This is a materially simpler situation than
Stage 18's `dfs` (where a worker only ever covers a fraction of the
tree, so proving UNSAT needs every worker to agree nothing is left
anywhere) or Options A/C's domain-splitting designs discussed in
`REPORT20.md`. The result: `cdcl.RunParallel`/`run_parallel` reuse
exactly the CAS-based single-winner shutdown from Stage 17's `hc`/`ws`
(a shared `stop` atomic, flipped by whichever worker's own
compare-and-swap succeeds first; every other worker notices at its own
next loop iteration and abandons its now-redundant search), with no
new coordination primitive at all.

The one adjustment from the `hc`/`ws` precedent: there, only a
*successful* (`Satisfiable`) result ever attempts the CAS, since `hc`/
`ws` are incomplete algorithms that can never prove UNSAT. Here, both
verdicts are equally authoritative, so the CAS instead guards on "did
this worker reach a genuine verdict at all" (`!result.TimedOut`) --
`TimedOut`'s meaning was broadened this stage to cover both "the time
limit was reached" and "a peer already won and this worker gave up
early," so a worker stopped by a peer never mistakenly wins the race
to report `UNSAT` for a search it never actually finished.

### `Run`/`run` restructured to match Stage 17/18's `runLoop` pattern

Both languages' `Run`/`run` were split into a thin wrapper (prints the
"cdcl: ..." banner and final verdict) around an unexported `runLoop`/
`run_loop` that does the actual solving and is `stop`/`export`/`peers`
-aware -- the exact same restructuring Stage 17/18 already applied to
`hc`/`ws`/`dfs`. `RunParallel`/`run_parallel` calls `runLoop`/
`run_loop` once per worker directly, so a multithreaded run prints
exactly one combined banner and verdict, not `numThreads` duplicates.

The Rust side needed one structural accommodation the Go side didn't:
`cdcl.rs`'s pre-existing design (see its own module doc comment,
predating this stage) is written as free functions operating on loose
local state specifically to sidestep Rust's borrow checker, rather
than a `struct` with `&mut self` methods the way `dfs.rs`/
`hillclimb.rs` are. Extracting `run_loop` kept that same free-function
style; the new `stop`/`export`/`peers` inputs are just three more
parameters threaded through, not a change in the module's overall
shape. `cdcl.rs` also moved to `cdcl/mod.rs` + `cdcl/clause_share.rs`
this stage, mirroring `dfs`/`hillclimb`'s existing directory-module
split.

### Continuous clause sharing: the validated design, ported over cleanly

`go_src/internal/cdcl/clauseshare.go` and
`rust_src/src/cdcl/clause_share.rs` are independent, from-scratch
reimplementations of the lock-free, atomic-pointer-swap ring buffer
validated in `util/clausesharing/` last stage (Go:
`atomic.Pointer[T]`; Rust: `arc-swap`'s `ArcSwapOption`) -- nothing
here imports that prototype, and nothing there is imported here, per
the `util/` constraint. No design changes were needed porting it from
the prototype's generic byte payloads to real `cnf.Clause`/`Clause`
values; the only genuinely new code is the glue connecting it to the
real search:

- **Export**: `addLearnedClause`/`add_learned_clause`'s caller
  publishes every newly learned clause of length <= 8 (ManySAT's own
  convention, per `REPORT20.md`) to this thread's own export buffer,
  immediately, the same conflict it was learned in -- not batched to
  a restart boundary, which is the whole point of Option B over the
  batched alternatives `REPORT20.md` considered and rejected. Unit
  clauses (length 1) are not shared; see `EXPORT_MAX_CLAUSE_LEN`'s doc
  comment for why that's a documented simplification, not an
  oversight -- a unit clause becomes a permanent level-0 fact applied
  directly, and sharing that usefully would need a different import
  path (assigning a literal permanently in a peer's trail) that this
  first implementation doesn't attempt.
- **Import**: `maybeImport`/`maybe_import`, called once per conflict
  from `learnAndBackjump`/inline in the conflict-handling code, gated
  to once every 32 conflicts (`importCheckMask`/`IMPORT_CHECK_MASK`,
  the same cheap bitmask trick `timeCheckInterval` already uses) to
  keep the O(numPeers) cost of a check bounded even at 128 threads.
  For each peer, at most one candidate clause is read per check --
  bounded work regardless of how fast a peer is learning.
- **`importClause`/`import_clause`**: the one genuinely new question
  importing raises that self-learning never does -- a clause derived
  from a *different* thread's search state says nothing about how its
  literals currently stand under *this* thread's assignment. Reusing
  `chooseWatch`/`choose_watch` (the same helper `newSolver`/
  `bootstrap` already uses to pick two watches for the original
  problem's clauses against a possibly-non-empty starting assignment)
  answers this safely: if two not-currently-false literals exist, the
  clause is added live, right now; if not, the import is simply
  dropped. A dropped import is never a correctness problem (losing a
  shared clause only costs a missed optimization, never soundness),
  so this sidesteps the much harder alternative -- treating an
  already-falsified imported clause as a conflict to analyze mid-
  decide -- for a case that resolves itself for free on a later check
  once the importing thread's own trail has moved on.
- Imported clauses get a fresh `0.0` activity score and count fully
  toward `estimatedBytes`, exactly like a self-learned clause -- so
  they compete for survival under `reduceClauseDatabase` on identical
  terms, and `REPORT20.md`'s memory-limit resolution (per-thread,
  `num_threads * memory_limit` in aggregate) holds with no new
  accounting: there is no periodic "merge everyone's database, filter
  to short clauses, redistribute" step to bound at all, since
  continuous per-clause import already only ever touches one
  already-filtered clause at a time.

## Command line arguments

`--alg-params` val2 (restart strategy) gains a fourth value; see
Restart strategy above and the updated help text (both languages) for
the exact default-selection rule. `--num-threads`/`-z`'s help text now
also describes `cdcl`'s portfolio-not-divide-and-conquer behavior. No
other CLI surface changed.

## Verification

- **Unit tests**: Go's `cdcl` package grew from 35 to 48 tests (a new
  `parallel_test.go`); Rust's grew from 178 to 190 total. New tests
  per language cover: the
  `numThreads=1` exact-compatibility guarantee; a satisfiable formula
  solved correctly at several thread counts (2/4/8/32); real and
  larger pigeonhole UNSAT proofs at up to 64 threads; a respected time
  limit; exactly-one-winner across 20 trials at 16 threads; the
  round-robin resolution order and that an explicit strategy always
  passes through unchanged; the export buffer's basic round-trip,
  never-written-slot, and non-aliasing behavior; and (Go only, since
  it needed a from-scratch `solver` to poke at directly)
  `importClause` correctly dropping an already-falsified import while
  still adding a live one. `go build ./...`, `gofmt -l .`, `go vet
  ./...`, `go test ./...` (9 packages) and `cargo fmt --check`, `cargo
  clippy --all-targets -- -D warnings`, `cargo test` (190 tests) are
  all clean.
- **Race detector**: `go test -race -count=10 ./internal/cdcl/...` is
  clean. Rust has no runtime equivalent, but no `unsafe` is used
  anywhere in the new code, so its ownership/`Send`/`Sync` rules rule
  out data races at compile time.
- **Cross-language sweep**: an independent Python script (not part of
  the repo; adapted from Stage 18/19's scratchpad verification
  scripts) ran both release binaries with `--algorithm=cdcl` across
  `--num-threads` in `{1, 4, 32}`, both `Vsids`/`Lrb` `SelectVar`
  values, and restart strategies `{polynomial, geometric, round-
  robin}` plus an explicit-override case, against `uf50-218`/
  `uuf50-218`/`uf175-753`/`uuf175-753` (420 runs: 0 Go/Rust
  mismatches, 0 verdicts disagreeing with SATLIB ground truth, 0 bad
  solutions) and separately against the new structured benchmarks
  (`blocksworld` minus two known-hard instances excluded from this
  correctness sweep, `flat125-301`, `flat200-479`, `ssa`: 80 runs, 0
  Go/Rust disagreements, 0 bad solutions -- no asserted ground truth
  for these, since it isn't encoded in the filename the way uf/uuf is,
  but both languages agreed on every verdict and every returned SAT
  assignment independently checked out).
- **Manual verification**: confirmed identical single-threaded output
  between Go and Rust (byte-for-byte, including the satisfying
  assignment); identical `restart=N` banner text across the default/
  round-robin/explicit-override cases described above; correct,
  matching SAT output at 128 threads between both languages.
- **A real concurrency sanity check worth naming explicitly**: early
  in benchmarking, two structured instances showed *no* wall-clock
  improvement at all from threading, which was surprising enough that
  I checked whether the workers were actually running concurrently at
  all (not just "not helping") before concluding anything. Timing
  instrumentation confirmed they were: all workers started and
  finished within microseconds of each other, and their own
  `runLoop` calls took ~30ms total, but the *whole program* took
  ~11s -- because Stage 8 preprocessing (untouched by this stage,
  single-threaded, running once before any algorithm-specific code)
  was the actual bottleneck on those two instances, confirmed
  directly by rerunning with `--no-preprocessing` (11s -> 34ms). See
  Benchmark below for what a properly search-bound instance shows.

## Benchmark: does it actually help?

Two of Stage 21's four new benchmark directories turned out not to be
useful for measuring *this* stage's work, for the reason above:
`blocksworld`'s `bw_large.c`/`bw_large.d` and the harder `ssa7552-*`
instances are dominated by single-threaded preprocessing time, not
search time, so no amount of search-phase threading shows up in their
wall-clock numbers. (`flat125-301`/`flat200-479` and the rest of
`blocksworld`/`ssa` solve in well under a second either way -- also
not useful for a scaling measurement, just for correctness, which the
sweep above already covers.) This isn't a defect in the new benchmark
files -- STAGE21.md said to use them "as you see best," and knowing
which ones are actually search-bound is exactly the kind of thing
worth finding out and reporting, not glossing over.

The existing `uuf250-1065` set (already known hard for `cdcl` search
specifically, and small enough that preprocessing is negligible) is
what actually shows the threading working. All runs below are on the
same 10-core machine as prior stages' benchmarks, default
`--alg-params` (Vsids, round-robin restart once `--num-threads > 1`):

`uuf250-01.cnf` wall-clock time by `--num-threads`:

| threads | Go       | Rust     |
|---------|----------|----------|
| 1       | 23.7 s   | 21.3 s   |
| 2       | 19.9 s   |    --    |
| 4       | 11.2 s   | 10.6 s   |
| 8       | 8.5 s    | 7.2 s    |
| 16      | 13.5 s   | 8.8 s    |

Both languages show a real, substantial speedup through 8 threads
(Go: ~2.8x; Rust: ~3.0x), then a regression at 16 on this 10-core
machine (oversubscription). A second instance (`uuf250-012.cnf`,
threads=1 vs. 8 only) showed an even larger gain: 22.6s -> 5.9s
(~3.85x), confirming this isn't a one-off. This is the genuine
Option-B benefit STAGE20.md/`REPORT20.md` predicted: portfolio
diversification (via the round-robin restart schedule) plus
continuous clause sharing letting the fastest-converging worker win,
with every other worker's partial progress (shared clauses) available
to whichever one gets there.

## Testing

- Go: `go build ./...`, `gofmt -l .`, `go vet ./...`, `go test ./...`
  (48 `cdcl` tests, 9 packages total), `go test -race -count=10
  ./internal/cdcl/...` are all clean.
- Rust: `cargo fmt --check`, `cargo clippy --all-targets -- -D
  warnings`, `cargo test` (190 tests) are all clean.
- Cross-language and manual verification as described above.

## Open questions / notes for you

- **Preprocessing is now the visible bottleneck on some real
  instances** (`bw_large.c`/`.d`, the harder `ssa7552-*` files):
  ~11s single-threaded, all of it before `cdcl`'s own (correctly
  parallel) search ever starts, confirmed via `--no-preprocessing`.
  Nothing in Stages 17-21 has threaded preprocessing itself. Whether
  that's worth a future stage depends on whether instances like these
  matter to you going forward -- flagging it now since it was a
  genuine surprise finding, not because I think it needs immediate
  action.
- `bw_large.c.cnf`/`bw_large.d.cnf` were excluded from the automated
  correctness sweep (not from the codebase or from manual testing --
  just from the scripted sweep's time budget) specifically because of
  the preprocessing cost above, not because of anything wrong with
  the search.
- Per `STAGE21.md`, a `CDCL` branch already exists at this point for a
  future Option D excursion if you want to revisit it; nothing here
  depends on or touches that branch.
- `SeqCst` used throughout every new atomic (the export buffer's
  indices, the shared `stop`/`winner`), consistent with the project's
  standing directive.
- The Go-hand-rolls/Rust-uses-a-crate asymmetry continues:
  `clauseshare.go` uses the standard library's `atomic.Pointer[T]`
  directly; `clause_share.rs` uses `arc-swap` (a new dependency this
  stage) for the same reason `util/clausesharing/rust` did --
  Rust has no GC, so an atomic pointer swap needs its own answer for
  safe reclamation, which `ArcSwapOption`'s `Arc` refcounting gives
  for free.
