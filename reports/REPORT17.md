# Report: Stage 17

## Summary

Stage 17 is complete for both the Go and Rust versions of `vibe_sat`:
`--algorithm=hc` and `--algorithm=ws` are now multithreaded, per
`STAGE17.md`'s decisions on top of `REPORT16.md`'s discussion. A new
`--num-threads`/`-z` flag controls the worker count (default 1,
oversubscription allowed); `hc`/`ws` split a given restart/try count
evenly across workers but hand each the full time limit unshared;
exactly one solution is ever reported even if multiple workers
succeed near-simultaneously; a shared best-score tracker keeps
`--verbose`'s "new best score" reporting meaningful across workers;
and every run now prints total wall-clock time at `--verbose >= 1`.
`dfs`/`cdcl` are untouched this stage, as `STAGE17.md` scoped it to
`hc`/`ws` only.

The one deliberate deviation from `STAGE16.md`'s literal wording:
`--num-threads` uses a hyphen, not the underscore `STAGE16.md`/
`STAGE17.md` both wrote it with, to match every other flag in this
program (confirmed with you before implementing).

## Design

### Threading model: `std::thread`, not tokio; no crossbeam yet

Per your confirmation of `REPORT16.md`'s recommendation, Rust uses
plain `std::thread` (specifically `std::thread::scope`, so worker
closures can borrow `problem`/`lists` directly instead of requiring
`'static` owned copies -- `thread::spawn` alone would have forced
that). You asked to use `crossbeam-channel` going forward, but this
stage's design -- `hc`/`ws` splitting into independent, unsupervised
workers -- needs no message passing between threads at all, only
shared atomics (a stop flag, a best score, and join handles to
collect results), so no channel crate is pulled in yet. It'll earn
its place once `dfs`/`cdcl`'s work-stealing protocol (`REPORT16.md`'s
"need work"/steal messages) actually needs one.

Go needs nothing new: goroutines and `sync`/`sync/atomic` were
already available.

### Splitting work: `Params`/`WalkSatParams` grow two fields

Both Go's `hillclimb.Params`/`WalkSatParams` and Rust's
`hillclimb::Params`/`WalkSatParams` gain two fields:

- `Stop` (Go `*atomic.Bool`; Rust `Option<Arc<AtomicBool>>`): checked
  once per restart, at the same point `NumStarts`/`TimeLimit` already
  are. `nil`/`None` (every single-threaded caller, including `Run`/
  `run` themselves) means it's never consulted.
- `BestScore` (Go `*atomic.Int64`; Rust `Option<Arc<AtomicI64>>`):
  replaces the existing local `bestScore` variable's role via a
  compare-and-swap loop, so several workers can share one "have we
  beaten the best score anyone's found so far" tracker instead of
  each keeping (and printing) its own possibly-stale one.

Both are optional and nil-safe by construction, which is what makes
the compatibility guarantee below straightforward rather than
something that needs separate enforcement.

### The `--num-threads=1` compatibility guarantee

Every existing single-threaded test, and every existing call site
that doesn't yet pass a thread count, needed to keep working
unchanged. Rather than trying to prove that by inspection, the new
entry points (`RunParallel`/`RunWalkSatParallel` in Go,
`run_parallel`/`run_walksat_parallel` in Rust) special-case
`numThreads <= 1` to call the existing `Run`/`RunWalkSat` (`run`/
`run_walksat`) directly, with the caller's `rng` untouched -- no
goroutine/thread, no derived sub-generator, no atomics constructed at
all. So single-threaded behavior isn't merely *equivalent* to before,
it's *the same code path*, which is what a new test in each language
(`TestRunParallelWithOneThreadMatchesRun`/
`test_run_parallel_with_one_thread_matches_run`, and the WalkSAT
analogs) checks directly: given the same seed, `RunParallel(problem,
..., 1, rng, ...)` and `Run(problem, ..., rng, ...)` are asserted to
produce bit-for-bit identical results.

For `numThreads > 1`, both `Run`/`run` and `RunWalkSat`/`run_walksat`
were split into a thin wrapper (prints the "hillclimb:"/"walksat:"
banner and the final "SAT"/"UNKNOWN" verdict) around an unexported
loop (Go: `runLoop`/`walkSatLoop`; Rust: `run_loop`/`walksat_loop`)
that does the actual searching and is `Stop`/`BestScore`-aware. The
parallel entry points call the loop directly, once per worker, and
print exactly one combined banner (with a `num_threads=N` prefix) and
one combined verdict themselves -- so a multithreaded run never
produces `numThreads` duplicate/interleaved announcement or verdict
lines, only the diagnostic lines (`--verbose >= 2`'s "new best score",
now genuinely global; `--verbose >= 3`'s "stuck"/"gave up", which stay
per-worker, since they're inherently per-restart events).

### Splitting `NumStarts`, not `TimeLimit`

Per your confirmation: if a restart/try count is given, each worker
gets `ceil(count/numThreads)` (so the total attempted across all
workers may run a little past the requested count, rounding up, never
short); a given time limit is handed to every worker in full,
unchanged, since the workers run concurrently and dividing it would
only shrink the wall-clock time actually spent without letting any
more work fit into it.

### Exactly one solution reported

Each worker's own local search still returns its own honest result.
What decides which one is *the* answer is a single
compare-and-swap on the shared `Stop` flag: the moment a worker's
local loop finds a satisfying assignment, it attempts to flip `Stop`
from false to true, and only the worker whose CAS actually succeeds
is recorded as the winner (Go: a shared `atomic.Int32` winner index;
Rust: an `Option<SolveResult>` written from inside the `thread::scope`
body after every worker's `JoinHandle` is joined). Every other
worker's own result -- even one that's also genuinely satisfying, from
a start that finished at nearly the same moment -- is discarded. This
is the same primitive that stops the other workers promptly (they
notice `Stop` at their own next restart), so "stop everyone else" and
"pick the one true answer" are the same atomic operation, not two
separate mechanisms that could disagree.

### Per-worker RNG

Sharing one RNG across workers was ruled out for the reasons discussed
in `REPORT16.md` (a data race in Go; simply doesn't compile in Rust,
since `Rng`'s methods take `&mut self`) and for reproducibility (a
mutex-guarded shared RNG would make the exact sequence of random
choices depend on scheduling, on top of already not knowing which
worker wins). Each worker instead gets its own generator, seeded by
drawing from the caller's `rng` *before* any worker starts (Go:
`rand.New(rand.NewPCG(rng.Uint64(), rng.Uint64()))` per worker; Rust:
`StdRng::seed_from_u64(rng.random::<u64>())` per worker). Since this
derivation happens sequentially in the calling goroutine/thread before
any concurrency begins, the whole set of per-worker seeds is itself
deterministic given `(rng`'s state, `numThreads)`, even though which
worker's result ultimately wins a race to a solution is not.

### Wall-clock timing and the oversubscription warning

Both are independent of threading and apply to every algorithm.
`main`/`main.rs` now records a start time at the very top of `main`
and prints `wall clock time: <duration>` at `--verbose >= 1` right
before every successful exit (both the "preprocessing alone proved
UNSAT" early exit and the normal end-of-algorithm exit go through one
shared `exit`/`exit` helper now, so there's exactly one place this is
printed from in each language). The oversubscription warning
(`--verbose >= 1` and `--num-threads` at least twice the logical core
count) is checked once, right after argument parsing, using
`runtime.NumCPU()` (Go) / `std::thread::available_parallelism()`
(Rust) -- informational only, since oversubscription itself is
allowed, not rejected.

## Command line arguments

```
--num-threads=<integer>, -z <integer>
```

Number of concurrent worker threads; default 1. Currently honored
only by `hc`/`ws`; accepted (and validated as a positive integer) for
every algorithm, but silently unused by `dfs`/`cdcl` until a later
stage extends threading to them. A value at least twice the detected
core count prints a one-line warning at `--verbose >= 1`, but is not
rejected -- oversubscription is explicitly allowed, per your
confirmation of `REPORT16.md`'s discussion.

## Verification

- **Unit tests**: Go's `hillclimb` package grew from 15 to 23 tests;
  Rust's grew from 15 (across `mod.rs`/`walksat.rs`) to 23. New tests
  per language cover: the `numThreads=1` exact-compatibility
  guarantee (both `hc` and `ws`); a satisfiable formula actually
  solved when split across several workers; an unsatisfiable formula's
  aggregate start count landing in the expected
  `[count, count+numThreads-1]` range from `ceil` rounding; the stop
  signal halting a loop before any work when pre-set; and the shared
  best-score CAS never reporting (or lowering) a value below what's
  already recorded. `go build ./...`, `gofmt -l .`, `go vet ./...`,
  `go test ./...` (9 packages) and `cargo fmt --check`, `cargo clippy
  --all-targets -- -D warnings`, `cargo test` (167 tests) are all
  clean.
- **Race detector**: `go build -race ./...` and `go test -race
  -count=20 ./internal/hillclimb/...` are clean. Rust has no runtime
  equivalent, but its ownership/`Send`/`Sync` rules rule out data
  races for this code at compile time (no `unsafe` is used anywhere in
  the new code) -- exactly the structural difference flagged in
  `REPORT16.md`.
- **Manual verification**: both release binaries were run side by side
  across single- and multi-threaded `hc`/`ws` invocations, at several
  `--verbose` levels, and with `--num-threads` values large enough to
  trigger the oversubscription warning. Confirmed: identical
  `--help` text; identical warning text; identical "hillclimb:
  num_threads=N ..."/"walksat: num_threads=N ..." banners; exactly one
  "SAT"/"UNKNOWN" line and one "wall clock time: ..." line per run,
  regardless of thread count; identical rejection of `--num-threads=0`
  in both languages. `hc`/`ws`'s actual solutions are not, and never
  have been, byte-comparable across runs or languages (see below), so
  that comparison wasn't attempted here.

## Testing

- Go: `go build ./...`, `gofmt -l .`, `go vet ./...`, `go test ./...`
  (9 packages, 24 `hillclimb` tests), and `go test -race -count=20
  ./internal/hillclimb/...` are all clean.
- Rust: `cargo fmt --check`, `cargo clippy --all-targets -- -D
  warnings`, and `cargo test` (167 tests) are all clean.
- Manual/scripted verification as described above.

## Open questions / notes for you

- **`hc`/`ws` were never byte-comparable across runs or languages to
  begin with**, threading or not: both seed their RNG from OS entropy
  (`crypto/rand` in Go, `rand::rng()` in Rust) with no `--seed` flag,
  so every invocation -- single- or multithreaded -- already produced
  a different random search before this stage. The exact-parity
  verification technique `REPORT16.md` flagged as at risk really
  applies to `dfs`/`cdcl` (whose default heuristics rarely exercise
  randomness at all, so their output has incidentally been
  reproducible), not to `hc`/`ws`; there's nothing to lose here that
  wasn't already unreproducible. It'll matter for real once `dfs`/
  `cdcl` are threaded.
- **The `go build -race`/stress-testing item you asked me to keep
  raising**: noted, and I'll include it whenever you ask "what should
  we do next" -- per your instruction, not attempted further (beyond
  this stage's own `-race` runs, which were cheap) this time.
- `--num-threads` is accepted and validated for every algorithm, not
  just `hc`/`ws`, but has no effect yet on `dfs`/`cdcl`; the help text
  says so explicitly. Let me know if you'd rather it be rejected
  outright for those two until they're actually threaded.
- The best-score CAS loop and the winner-selection CAS both use
  `SeqCst` ordering throughout, the simplest-to-reason-about choice
  for a handful of infrequent cross-thread signals; nothing here is
  hot enough (checked once per restart, not once per flip) to be worth
  a weaker ordering.
