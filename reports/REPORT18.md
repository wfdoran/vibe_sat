# Report: Stage 18

## Summary

Stage 18 is complete for both the Go and Rust versions of `vibe_sat`:
`--algorithm=dfs` is now multithreaded, per `STAGE18.md`'s answers to
`REPORT16.md`'s open design questions. `dfs` reuses the
`--num-threads`/`-z` flag introduced in Stage 17 unchanged -- no new
CLI surface. The parallel search is a genuine divide-and-conquer
search, not a portfolio solver: a short breadth-first phase seeds up
to `num-threads` disjoint starting branches, and the worker threads
then explore those branches concurrently, stealing work from each
other's opposite deque end when they run dry. `--algorithm=cdcl` is
untouched this stage, as `STAGE18.md` scoped it to `dfs` only.

Before touching `go_src`/`rust_src`, I built a standalone,
language-agnostic termination-detection prototype in a new top-level
`util/termination/` directory (Go and Rust versions), per your
explicit authorization to use `util/` as a playground with the one
hard constraint that neither `go_src` nor `rust_src` may depend on
it. The prototype exists to validate the termination-detection
protocol's correctness under stress (up to 128 simulated workers) with
throwaway "task" objects before betting the real search on it; both
versions passed everything I threw at them, and the real
implementations in `dfs/parallel.go`/`dfs/parallel.rs` re-derive the
same protocol independently (no shared code, per the constraint), not
by importing the prototype.

In the process of writing this stage's own tests, I found and fixed a
pre-existing latent bug in the single-threaded `Run`/`run` (present
since `dfs` was first written, not introduced by this stage): a
formula fully solved by bootstrap's own unit propagation alone (e.g.
a formula made entirely of unit clauses) crashed with an index-out-
of-range panic in Go (and would have panicked via `.expect(...)` in
Rust) because nothing checked whether the root was already fully
assigned before asking `SelectVar` to pick a next variable to branch
on. Fixed in both languages, in the shared single-threaded path, not
just the new parallel code.

## Design

### The termination-detection prototype (`util/termination/`)

The hardest part of `STAGE18.md`'s design -- "we do have shared
memory" -- is deciding, correctly, when a work-stealing search with no
central coordinator is actually done: every worker is idle, and
nobody can ever hand out more work. A worker that decrements a shared
"how many workers are still doing something" counter the instant its
own deque runs dry is wrong: another worker, still busy, could hand
it fresh work moments later, and the counter would already have
dropped to zero on the strength of a stale observation.

The insight that makes this solvable: **new work can only ever be
created by a worker that is currently processing something it already
holds.** So a worker only needs to decrement the shared counter
*once*, and only *after* it has swept every peer looking for work and
found all of them empty -- not once per failed steal attempt against
a single peer. If the counter reaches exactly zero at that point,
nobody is doing anything, and since nobody is doing anything, nobody
can create more work: the search is genuinely finished. A second-order
race remains even so -- the *decrementing* worker's own "everyone I
just checked was empty" snapshot could itself be stale by the time the
counter hits zero -- so the decrement takes a `recheck()` callback,
invoked exactly once and only when the decrement brings the count to
precisely zero: if `recheck()` also finds every peer empty, termination
is confirmed; if not, the decrement is undone (the counter is
incremented back) and the worker goes back to stealing.

This became a `Terminator`/`WorkerState` pair in both languages:
`Terminator.decrement(recheck) -> (terminated, stillIdle)` /
`Terminator.increment()` hold the shared atomic counter;
`WorkerState` wraps them with a local `isActive` flag so a worker can
call `MarkActive()`/`MarkIdle(recheck)` redundantly (e.g. on every loop
iteration) without ever double-counting itself.

I validated this with an abstract simulation before writing any real
search code: `simulate.go`/`simulate.rs` spawns `numWorkers` workers
around a shared pool of randomized "tasks" (each with a shrinking
budget guaranteeing the simulation is finite; each processed task has
a 30% chance of pruning, 40% of spawning one child, 30% of spawning
two), starting all the work in a single worker's deque so every other
worker begins idle and must steal to get going at all -- the worst
case for the protocol, not the easiest. `terminator_test.go`/the
`#[cfg(test)]` module in `lib.rs` stress-test this across worker
counts `{1, 2, 3, 4, 8, 16, 32, 64, 128}` x 20 random seeds each (180
combinations), asserting every task is processed exactly once and the
simulation actually terminates (a 10-second per-run timeout catches a
hang rather than letting the test suite itself hang). Both passed
cleanly, including Go under `go test -race -count=5` and Rust rebuilt
and rerun 3x in release mode.

`util/termination/go/deque.go` is a mutex-guarded double-ended deque;
`util/termination/rust/` uses `crossbeam-deque` directly, per your
explicit endorsement ("The crossbeam crate is well-tested"). Both
`util/` trees have their own `go.mod`/`Cargo.toml` and are not
referenced from `go_src`/`rust_src` anywhere -- I'm leaving them in
the repo per your instruction that anything left in `util/` is
assumed wanted, but haven't committed them myself, per the project's
standing rule that I never commit.

### Chase-Lev double-ended structure

Confirmed by you directly: each worker treats its own deque as a
LIFO stack (`pushBottom`/`popBottom` -- work it just created from
branching is the first thing it explores next, for good
depth-first locality); any *other* worker stealing from it takes from
the opposite end, FIFO (`stealTop`) -- the oldest, least-recently-
touched branches, which tend to be large and unexplored, rather than
the tiny sliver of tree the owner just carved off for itself. Go
hand-rolls this (`go_src/internal/dfs/deque.go`, a
`sync.Mutex`-guarded slice with the same four operations as the
prototype, independently reimplemented for the real `searchNode`
type -- not importing `util/`); Rust uses `crossbeam_deque::Worker`/
`Stealer` directly in `dfs/parallel.rs`, per your endorsement.

### BFS seeding: `{SAT, UNSAT, UNKNOWN}`

Per `STAGE18.md`'s explicit instruction, the seeding phase
(`bfsSeed`/`bfs_seed`) returns one of three outcomes:

- **SAT**: some branch, expanded during seeding itself, turned out to
  already be a complete satisfying assignment. Returned immediately;
  no worker threads are ever spawned.
- **UNSAT**: the seeding queue emptied out (every branch it tried
  was pruned by `BCP`/`bcp` reporting a contradiction) before reaching
  `num-threads` entries. Also returned immediately, with the same
  "no threads spawned" shortcut -- this is a real, if unlikely,
  possibility for a tree that's small relative to the requested
  thread count, and there's no reason to spin up workers to explore
  nothing.
- **UNKNOWN**: the ordinary case -- the queue reached `num-threads`
  entries first. The seeds (1 to `num-threads` of them; see below)
  are handed one-per-worker to the parallel phase, exactly as
  `STAGE18.md` describes: "the vast majority of the time it returns
  UNKNOWN and you carry on with the multi-threaded depth-first
  search."

The BFS itself is a plain FIFO queue seeded with the (already
bootstrapped/unit-propagated) root: pop the front, branch it on
`SelectVar`, push whichever of the two children `BCP`/`bcp` didn't
prune to the back, and stop as soon as the queue has `num-threads`
entries or is empty.

**Fewer seeds than threads needs no special-case code.** If the whole
tree has fewer leaves than `num-threads` (small formula, large thread
count), the seeding loop simply empties out below the target and
returns UNSAT directly (as above); if it empties out exactly *at* some
smaller number of branches still pending exploration, `RunParallel`/
`run_parallel` spawns exactly `len(seeds)` workers, not
`num-threads` of them -- a worker started with genuinely nothing to
do just immediately begins the idle-and-steal loop, which the
termination protocol already handles correctly (that's precisely the
"everyone starts idle" case the `util/` simulation stress-tested).

### The pre-existing bootstrap-completion bug

`TestBFSSeedReturnsSATDirectlyWithoutSpawningWorkers`, a test I wrote
to exercise the "SAT during seeding" path above, needed a formula
guaranteed to be fully solved before any branching happens at all --
the simplest way to construct one is a formula made entirely of unit
clauses (e.g. `{1}, {2}`), since `bootstrap()`'s own unit propagation
alone finishes it. Running that test crashed: `SelectVar` was called
on the already-fully-assigned root, returned `-1` (Go's sentinel for
"no unassigned variable exists"), and the caller then indexed
`branchAssignment[-1]`, panicking. Rust's `select_var` would have hit
its own `.expect(...)` panic on the same input.

This is a genuine pre-existing bug in `Run`/`run`, not something Stage
18 introduced -- it was just never exercised before, because no
existing test happened to construct a formula solved by bootstrap
alone, and every *non-root* node ever created during search is only
ever produced by `BCP`/`bcp` explicitly reporting `OK`/`Status::Ok`
(never `Done`/`Status::Done`), which by construction guarantees it
still has an unassigned variable left. Only the root can ever have
this property. Fixed with a new `allAssigned(x)`/`all_assigned(x)`
helper and an explicit check-and-immediate-SAT-return placed right
after `bootstrap()` succeeds, in **both** `Run`/`run` (the
single-threaded path, since it's reachable from the public API
regardless of this stage) and the new `bfsSeed`/`bfs_seed`.

### Single-winner protocol (reused from Stage 17)

Same primitive as `hc`/`ws`: the moment any worker's local search
returns a satisfying assignment, it attempts
`stop.CompareAndSwap(false, true)` (Go) /
`stop.compare_exchange(false, true, SeqCst, SeqCst)` (Rust); only the
winning CAS records itself in a shared winner index. The one
Rust-specific wrinkle worth flagging: this check has to happen
*inside* each worker's own `scope.spawn(move || { ... })` closure,
immediately after `dfs_worker(...)` returns within that same running
thread -- not deferred until a later, sequential `.join()` loop over
all the handles. I actually wrote it the second, wrong way on my
first pass (mirroring the *shape* of `thread::scope`'s join-everything
idiom without noticing it defeated the whole point of the stop
signal: a fast worker's win wouldn't reach the other, still-running
workers until the orchestrator happened to join that worker's handle
in sequence). Caught by re-deriving from Go's structure, where the CAS
plainly runs inside each concurrently-executing goroutine, and fixed
by moving Rust's CAS inside the spawned closure to match.

### Per-worker RNG, thread count, and memory ordering

Same as Stage 17: sub-seeds are drawn sequentially from the caller's
`rng` before any worker starts, so the whole run is reproducible given
`(seed, num-threads)` even though which worker wins is not. Thread
counts are not capped at any small number -- both implementations were
exercised up to 128 threads (per `STAGE18.md`'s "should scale to 128
threads, maybe more" note) with no crashes, hangs, or incorrect
verdicts. Every atomic in the new code (`stop`, `winner`, `timedOut`,
the terminator's counter) uses `SeqCst` exclusively, per your explicit
direction to keep using it "even in hot code," deferring any
acquire/release optimization to a future session.

## Command line arguments

No changes. `--num-threads`/`-z` (Stage 17) now also governs `dfs`;
its help text in both languages was updated to describe `dfs`'s
divide-and-conquer/work-stealing behavior alongside `hc`/`ws`'s
existing restart-splitting description, including the "may use fewer
than num-threads threads" note.

## Benchmark: does it actually help?

This benchmark set turned out to have an awkward gap for plain
(non-CDCL) `dfs`: every `uuf100-430` instance I tried finishes in
well under a second single-threaded (too easy to show scaling), while
every `uuf250-1065` instance I tried still hadn't finished proving
UNSAT after 120 seconds single-threaded, even at 32 threads (too hard
--- unsurprising, since without clause learning, proving
unsatisfiability requires something close to the full exponential
tree, and parallelism only divides that tree's exploration across
workers, it doesn't shrink it). So this stage's speedup numbers are
from the `uf250-1065` **satisfiable** instances instead, where the
search is a parallel-OR (stop as soon as *any* worker finds a
satisfying leaf), which does complete in a measurable, if noisier,
amount of time. All runs below are on a 10-core machine, 3 repeats per
cell (repeats agreed within a few percent of each other, so only one
is shown):

`uf250-0100.cnf` wall-clock time by `--num-threads`:

| threads | Go       | Rust     |
|---------|----------|----------|
| 1       | 25.5 s   | 9.0 s    |
| 2       | 10.2 s   | 3.5 s    |
| 4       | 2.8 s    | 0.66 s   |
| 8       | 4.6 s    | 0.84 s   |
| 16      | 7.4 s    | 1.6 s    |

Both languages show a clear, large speedup from 1 to 4 threads (Go:
~9x; Rust: ~13x -- superlinear, since parallel-OR search can get
lucky and hit a satisfying branch sooner than single-threaded search
would explore it in the same order), then get *worse* going from 4 to
8 to 16 on this 10-core machine. I did not chase this down further,
but the likely causes: (a) oversubscription/lock contention on the
hand-rolled Go deque (Rust's `crossbeam-deque` is lock-free, and shows
the same shape but less severely) once thread count exceeds the
physical core count, and (b) `bfsSeed`'s shallow BFS phase itself
costs a little more wall time to produce more seeds, for no benefit
once there are already enough seeds to keep every core busy.
Un-timed spot checks on `uf250-01.cnf` (a smaller, easier instance)
showed the same qualitative shape.

I did not attempt to smooth out or explain away the non-monotonicity
above 4 threads -- it's the honest result on this hardware and this
benchmark set, and I'd rather show you the real numbers than a
cherry-picked run. Let me know if you'd like a deeper dive here (e.g.
`GOMAXPROCS`/thread-affinity experiments, or profiling the deque
contention) before moving on.

## Verification

- **Unit tests**: Go's `dfs` package grew from 21 to 33 tests; Rust's
  grew from 21 (`dfs/mod.rs`) to 32 (`dfs/mod.rs` + 11 new in
  `dfs/parallel.rs`). New tests per language cover: the
  `numThreads=1` exact-compatibility guarantee; a satisfiable formula
  solved correctly at several thread counts (2/4/8/16/32/64); real
  and larger pigeonhole UNSAT proofs at up to 64 threads; the BFS
  seeding phase's SAT/UNSAT-without-spawning-workers shortcuts; the
  fewer-seeds-than-threads case on a deliberately tiny tree with 50
  requested threads; the deque's push/pop/steal consistency; the
  terminator's quiescence-detection correctness and idempotent
  `MarkIdle`; a respected time limit; and exactly-one-winner across
  20 trials at 16 threads. `go build ./...`, `gofmt -l .`, `go vet
  ./...`, `go test ./...` (179 tests across 9 packages) and `cargo
  fmt --check`, `cargo clippy --all-targets -- -D warnings`, `cargo
  test` (178 tests) are all clean.
- **Race detector**: `go test -race -count=10 ./internal/dfs/...` is
  clean. Rust has no runtime equivalent, but no `unsafe` is used
  anywhere in the new code, so its ownership/`Send`/`Sync` rules rule
  out data races at compile time.
- **`util/` prototype**: see above -- 180 worker-count/seed
  combinations up to 128 simulated workers, in both languages,
  confirming the termination-detection protocol itself before it was
  reimplemented for the real search.
- **Cross-language sweep**: an independent Python script (not part of
  the repo; adapted from this project's existing scratchpad
  verification scripts) ran both release binaries with
  `--algorithm=dfs` across `--num-threads` in `{1, 2, 4, 8, 32, 128}`
  and both `SelectVar` variants, against 20 real SATLIB benchmark
  files (`uf50-218`/`uuf50-218`/`uf100-430`/`uuf100-430`, mixing SAT
  and UNSAT) -- 260 total runs. Result: 0 Go/Rust disagreements, 0
  verdicts disagreeing with SATLIB's `uf`=SAT/`uuf`=UNSAT ground
  truth, and 0 SAT solutions that failed an independent from-scratch
  clause-satisfaction check.
- **Manual verification**: confirmed identical single-threaded output
  between Go and Rust; correct UNSAT verdicts at 8 threads on a real
  benchmark file; correct, matching SAT verdicts at 128 threads
  between both languages, including the `"dfs: num_threads=128
  select_var=..."` banner text matching exactly.

## Testing

- Go: `go build ./...`, `gofmt -l .`, `go vet ./...`, `go test ./...`
  (179 tests across 9 packages), `go test -race -count=10
  ./internal/dfs/...` are all clean.
- Rust: `cargo fmt --check`, `cargo clippy --all-targets -- -D
  warnings`, `cargo test` (178 tests) are all clean.
- Cross-language and manual verification as described above.

## Open questions / notes for you

- **The benchmark's awkward gap** (uuf100 too easy, uuf250 too hard
  for plain `dfs` to prove UNSAT in any reasonable time, threaded or
  not): this isn't a Stage 18 problem to fix, but it does mean I
  couldn't show you a clean parallel-*AND* (all workers must exhaust
  their share) speedup number, only parallel-*OR* (SAT, stop at the
  first hit) numbers. `cdcl`'s clause learning is what actually makes
  250-variable UNSAT tractable in this project; plain `dfs` was never
  going to scale there even multithreaded.
- **Non-monotonic speedup past 4 threads on a 10-core machine**: real,
  not a bug I could find, and not chased down further this stage (see
  the benchmark section above). Worth a closer look if `dfs`
  performance at high thread counts matters to you.
- `util/termination/` is left in the repo per your instruction ("I
  will assume you want committed" for anything left there); I have
  not committed it myself.
- `SeqCst` used throughout every new atomic, per your direction;
  nothing here was changed to a weaker ordering.
- `--num-threads` help text was updated in both languages to describe
  `dfs`'s work-stealing behavior; `cdcl` still explicitly says it
  ignores the flag "for now."
