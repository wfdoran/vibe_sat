# Report: Stage 32

## Summary

`STAGE32.md` asked me to revisit `REPORT29.md`'s Finding 1 (`dfs` OOM-killed
at 12.9 GB on a 415 MB, 17.7-million-clause file, despite a 3-second time
limit): confirm the diagnosis, and fix it if a fix exists. You correctly
worked out the root cause and the fix yourself in the stage prompt: `dfs`
clones a full assignment *and* an `O(clauses)` watch-state array for every
single branch pushed onto its search stack, and neither clone is actually
necessary — a single shared, incrementally-backtracked assignment (an
explicit trail recording which variables were assigned at each depth,
unassigned on backtrack) does the same job without the copy, exactly like
`cdcl` has done since Stage 11. Your instinct about the watches was also
correct: they never need cloning or undoing either, since backtracking only
ever turns assigned literals back into unassigned ones, and a watch that
was valid (non-false) at some point remains valid at any less-constrained
ancestor state too.

**This is implemented, in both languages, exactly as you described**:

- A shared, mutated-in-place assignment and watch state, per sequential
  exploration (`searchState`/`SearchState`), with an explicit trail and an
  explicit frame stack replacing the old per-branch clone-and-push.
- `BCP`/`bcp` now records every variable it force-assigns onto that trail
  (not just the caller's own branch decision), exactly as you anticipated
  ("BCP will have to be updated to record variables it assigns").
- The old full-clone type (`searchNode`/`SearchNode`) still exists, but is
  now used only where a genuinely independent snapshot is actually needed:
  a parallel run's initial per-worker seeds, and a worker's occasional,
  deliberate sharing of one spare branch with an idle peer — both rare
  compared to the total number of nodes explored, unlike every branch.

**Direct measurement confirms the fix**: reproducing `REPORT29.md`'s exact
repro command on the same 415 MB file, memory now stabilizes and stays
bounded (~4.1-4.3 GB in Go, ~2.7-3.0 GB in Rust) indefinitely, instead of
growing without bound to 12.9 GB and getting OOM-killed.

**This stage also found and fixed two real bugs during implementation** —
one a straightforward liveness bug in the new parallel work-stealing design,
the other a genuinely surprising performance regression discovered only
through direct benchmarking, not code review, that would have shipped
unnoticed without it. Both are described in full below, since "profile
first, then fix" (this project's own established practice) is exactly what
caught the second one — synthetic pigeonhole tests never revealed it; a
real SATLIB benchmark file did.

## The fix: `searchState`, a shared trail instead of a clone per branch

`searchState` (Go) / `SearchState` (Rust) holds one assignment, one
watch state, an explicit `trail []int` (every variable assigned since the
exploration began, decision or forced, in order), and an explicit stack of
`frame`/`Frame` values — one per active decision level, each recording just
`{variable, mark, next}`: `mark` is the trail length immediately before
that frame's variable was first assigned, and `next` is which of
`False`/`True` remains to be tried (or `Exhausted`, meaning pop this
frame). Backtracking is `undoTo(mark)`: walk the trail back to `mark`,
setting each of those variables back to `Unassigned`, then truncate. No
clone, no restore — there was only ever one assignment and one watch state
for the whole exploration.

This mirrors `cdcl`'s design (`trail`/`trailLim`/`backtrackTo`) closely,
adapted to `dfs`'s simpler chronological (no clause-learning, no
non-chronological jumps) backtracking: a `frame` is `cdcl`'s decision level,
just without a reason clause or an activity-based restart schedule.

`BCP`/`bcp` gained one new parameter, a trail to append to, used *only* for
variables it force-assigns via propagation — the caller (`step`) is
responsible for pushing its own branch-decision variable itself, exactly as
you anticipated. This is the one required change to BCP for the whole
redesign to work: without it, `undoTo` would have no way to know which
variables to unassign on backtrack.

### Why the watches never need cloning either

Your own reasoning in `STAGE32.md` ("if they were UNKNOWN when you start
backtracking, they will stay UNKNOWN") is exactly right, and is already
proven correct elsewhere in this codebase: `cdcl`'s `backtrackTo` doc
comment makes the identical argument, since `cdcl` has relied on it since
Stage 11. `isFalse`/`is_false` treats an `Unassigned` variable as "not
false" by definition, and backtracking only ever *removes* constraints
(assigned → unassigned), never adds a new false value out of nowhere — so
a literal that was valid to watch (non-false) under some assignment remains
valid under any less-constrained ancestor of that assignment, for either of
two reasons: the watched variable was fixed before the ancestor point (so
its value is identical in both), or it was fixed only afterward (so the
ancestor sees it as `Unassigned`, which is automatically "not false"
regardless of what it later becomes). `BCP` never re-validates an existing
watch outside the specific falsification it's reacting to, so this
invariant is all that's needed — a single watch state, mutated forward
only, is always internally consistent.

### What still gets cloned, and why that's now rare instead of universal

`searchNode`/`SearchNode` (the old, expensive type) is still exactly as
expensive to build as it always was, but is now used in exactly two places:

- **`bfsSeed`/`bfs_seed`**: unchanged from Stage 18 — a one-time breadth-
  first expansion producing at most `numThreads` seeds before any worker
  starts. This was never the source of the OOM (`numThreads` clones total,
  not one per node), so it needed no redesign.
- **`shedFrame`/`shed_frame`** (new this stage): the mechanism a parallel
  worker uses to make its own spare capacity available to an idle peer,
  described next.

## The parallel design: shed rarely, not per branch

A worker's ordinary operation never touches its deque at all: it runs its
own local `searchState` end to end, exactly like `Run`/`run` does, until
that local exploration is fully exhausted (`stepUNSAT`), at which point it
looks for more work exactly like a freshly-idle worker always has (its own
deque, then stealing from peers, then participating in termination
detection). The only new piece is *how* work becomes available to steal in
the first place, since the old design's "push every branch to the deque"
no longer happens.

`shedFrame`/`shed_frame` looks for the shallowest frame in the worker's own
stack whose `False` branch has been committed to but whose `True` branch
hasn't (i.e. `next == TryTrue`), and hands that branch off: it builds an
independent assignment covering only the ancestor variables locked in
before that frame (`trail[:mark]`), clones the *current* watch state for it
(safe by the same invariant above — the current, more-advanced watch state
remains valid for this less-constrained ancestor assignment too), runs
`BCP` once on the new decision, and pushes the result as a self-contained
`searchNode` onto its own deque for a peer (or itself, later) to pick up.
The shed-from frame is marked `Exhausted` immediately, so the worker never
retries what it just gave away.

A worker only ever considers doing this when its own deque currently looks
empty (checked cheaply, as part of the same periodic pause that also checks
the shared stop/deadline signals) — this is what keeps `shedFrame` rare in
practice rather than a clone-per-branch cost reintroduced under a different
name.

## Two bugs found during implementation

### The initial-seed-steal race (a liveness bug, found via stress-testing)

An early version of `dfsWorker`/`dfs_worker` treated "my own deque doesn't
have my own initial seed in it" as unreachable, since `RunParallel`/
`run_parallel` always pushes exactly one seed per spawned worker before
spawning it — and simply returned early if that assumption seemed to fail.
It's not actually unreachable: nothing orders "worker N's goroutine/thread
starts running" before "some other, already-running worker's steal sweep
reaches worker N's deque," so a peer can steal a worker's own seed before
that worker ever gets scheduled to pop it itself. When that race's loser
hit the "unreachable" path and returned silently, it permanently
undercounted the shared terminator's active-worker total by one — since
that decrement never happened, the count could never reach zero, and
global termination could never be confirmed. Reproduced intermittently
(roughly half of runs) via a stress loop of `TestRunParallelProvesUnsatisfiablePigeonhole`,
and confirmed via a `SIGQUIT` goroutine dump showing exactly one worker
left alive, permanently spinning against a fully torn-down, provably-empty
set of deques.

The fix: route the initial pop through the exact same `findWork`/
`find_work` retry-and-terminate-detect loop used for every later
exhaustion, instead of a separate "this can't happen" branch. Whichever
worker loses the seed-steal race just looks for other work immediately,
exactly like any other momentarily-idle worker would.

### The shed-frequency regression (a performance bug, found only by benchmarking)

This is the more important finding, and the reason this report leads with
"profile first" advice matters even for a stage that isn't primarily about
performance. Every unit test in this project's `dfs` suite — including a
64-thread, 30-variable pigeonhole stress test explicitly designed to
exercise stealing — passed throughout development. Direct measurement
against a real SATLIB benchmark file (`benchmark/uuf175-753/uuf175-083.cnf`,
the same file `REPORT19.md` and `REPORT26.md` used to measure `dfs`'s
parallel UNSAT scaling) told a completely different story:

| threads | before this fix | after this fix |
|---|---|---|
| 1 | 3.3s | 3.3s |
| 2 | *(25s timeout, no verdict)* | 1.8s |
| 4 | *(25s timeout, no verdict)* | 1.6s |
| 8 | *(25s timeout, no verdict)* | 1.5s |
| 16 | *(25s timeout, no verdict)* | 1.3s |

At the original shed-check interval (every 256 nodes), every thread count
above 1 got dramatically *slower* than sequential, hitting a 25-second
safety cutoff without finishing rather than the ~1.6 seconds
`REPORT19.md` originally measured for 2 threads on this exact file — and
letting one run continue unbounded (past 90 seconds, 1.5+ million nodes
explored per worker) confirmed it wasn't merely slow, it was still
actively diverging.

**This is not a correctness bug** — every shed subtree is still explored
exactly once, and every cross-language sweep this stage ran found 0
verification failures and 0 cross-language mismatches throughout. The cost
is real but specific to this stage's design: shedding hands a subtree to a
*freshly started* search, which calls `SelectVar`'s weighted heuristic
(and its rng-driven tie-breaking) starting over from that point, rather
than inheriting whatever sequence of choices the original, continuous
exploration would have made. For a heuristic this sensitive to tie-break
luck — and the weighted heuristic's own doc comment already describes it as
one that "tends to keep the search tree small" precisely *because* of the
choices it makes — restarting it often enough can turn a well-behaved
search into a much larger one, seemingly at random with respect to which
instances are vulnerable.

I confirmed this experimentally, not just by inspection: disabling shedding
entirely dropped the 2-thread case back to 1.86 seconds and ~40,000 total
nodes (matching single-threaded almost exactly); reducing shed frequency by
256x alone reproduced the same recovery. **A first fix attempt — rebuilding
a genuinely fresh watch state for the shed snapshot instead of cloning the
current (more-advanced) one, on the theory that a stale clone was somehow
causing weaker propagation — measurably helped (roughly 5-10x less
blowup) but did not fully fix it, and introduced a latent crash I caught
before it shipped**: `newWatchState`/`new_watch_state` requires two
non-false literals per clause, which a mid-search ancestor assignment can
violate perfectly validly (a clause already satisfied by one true literal,
with every other literal since turned false by later decisions — completely
fine for the incrementally-maintained clone, which never needs to
"re-justify" an existing watch, but fatal for a from-scratch rebuild). I
reverted that attempt entirely in favor of the frequency fix, which needs
no watch-construction change at all and carries no such risk.

The actual fix: increase the shed-check interval by roughly 4,000x (from
every 256 nodes to roughly every one million). This has no effect on
memory safety whatsoever — an unshed frame costs a few bytes on the
worker's own stack, not a clause-sized clone, regardless of how rarely
shedding fires — so this stage's core fix is untouched by this change; it
only affects how aggressively workers rebalance load, and this project has
only this one measurement of how badly the aggressive setting can go wrong,
not a principled way to pick an optimal middle ground under this stage's
time budget. I chose to err conservative (rare shedding, closer to "static
`bfsSeed` partition, rebalanced only for truly enormous searches") rather
than search further for a value that's merely "probably fine" on more
instances.

**This fix measurably helps, not just avoids regressing**: re-running this
stage's own cross-language sweep (`--algorithm=dfs`, `uf250`/`uuf250`, 8
threads) after the fix solved more files within the same time budget than
before it — Go went from 8/20 to 10/20 files solved, Rust from 10/20 to
15/20 — with 0 verification failures and 0 cross-language mismatches in
both sweeps.

## Verification

- **Real-file memory**: reproducing `REPORT29.md`'s exact command
  (`vibe_sat -i <415MB file> -a dfs -v 1 -t 3 -x`) directly, monitoring
  resident set size once per second: memory now stabilizes (Go: ~4.1-4.3
  GB; Rust: ~2.7-3.0 GB, oscillating within that band but never trending
  upward) instead of climbing without bound to the 12.9 GB `REPORT29.md`
  measured before getting OOM-killed. The process still doesn't respect
  `-t 3` promptly on this file — that's `REPORT29.md`'s own, separately
  documented "coarse time-check interval" finding (Finding 2/3: the search
  only checks the clock every 4096 nodes, and a single node's cost can
  itself run into milliseconds on a formula this large), explicitly
  outside this stage's scope.
- **Unit tests, both languages**: every pre-existing `dfs`/`parallel` test
  passes unchanged (behaviorally — some needed updating for `BCP`/`bcp`'s
  new trail parameter). New tests: trail-recording assertions in the BCP
  unit-chain test (Go and Rust both), and a new
  `TestShedFrameMaterializesUntriedSiblingForStealing`/
  `test_shed_frame_materializes_untried_sibling_for_stealing` that directly
  drives `shedFrame`/`shed_frame` to completion and checks the materialized
  snapshot's assignment, the shed frame's own state, and that the snapshot
  is independently solvable to a correct, verified verdict — necessary
  because the shed-check interval is now large enough that no small test
  problem would ever reach it organically.
- **Race detector**: `go test -race -count=10 ./internal/dfs/...` clean.
- **Stress testing**: the parallel test suite (including the
  liveness-bug-triggering `TestRunParallelProvesUnsatisfiablePigeonhole`)
  run in a loop — 60+ iterations after the seed-steal fix, several more
  rounds of 15-40 after the shed-frequency fix — with zero failures, after
  having reproduced the original race in roughly half of a similar-sized
  loop before the fix.
- **Cross-language sweeps** (`util/benchcompare`, `--algorithm=dfs`,
  `uf250-1065`/`uuf250-1065`, both `--num-threads=1` and `--num-threads=8`,
  before and after the shed-frequency fix): 0 verification failures, 0
  cross-language verdict mismatches, in every sweep.
- `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./...` clean;
  `cargo build --release`, `cargo clippy --all-targets -- -D warnings`,
  `cargo fmt --check`, `cargo test --release` (198 tests) clean.

## Answering `STAGE32.md`'s other questions

**"Is there some other reason why the memory is blowing up?"** No — the
per-branch clone fully explains it, confirmed by direct measurement rather
than assumption: removing it (and only it) is what took memory from
"climbs to 12.9 GB and dies" to "stabilizes around 4 GB and stays there."
The arithmetic was always consistent with this too: 17.7 million clauses ×
2 watched literals each is enough on its own for a single clone to run
100+ MB, and the old design made one such clone (plus an assignment clone)
for *every* branch pushed onto the stack — a search stack routinely holding
even a few dozen such entries before the next 4096-node time-check
trivially explains 12.9 GB.

**Non-monotonic UNSAT scaling (`REPORT22.md` item 20)**: substantially
already resolved by `REPORT26.md`'s lock-free deque rewrite, which found
the dramatic "plateaus at 4, gets worse at 8/16" pattern `REPORT18.md`/
`REPORT19.md` originally reported didn't reproduce on this (virtualized)
machine even before this stage — attributed there to mutex contention
specific to the original bare-metal machine, not a fundamental property of
the algorithm. This stage's own scaling measurement, taken as a direct
side effect of diagnosing the shed-frequency bug above (on the identical
file `REPORT19.md`/`REPORT26.md` used), confirms this again post-fix:

| threads | Go | Rust |
|---|---|---|
| 1 | 3.3s | 0.72s |
| 2 | 1.8s | 0.40s |
| 4 | 1.6s | 0.39s |
| 8 | 1.5s | 0.29s |
| 16 | 1.3s | 0.27s |

Smooth and monotonic in both languages, all the way to 16 threads — no
non-monotonic dip in either. I did not do any deeper profiling of this
question beyond what fell directly out of the memory-fix investigation, per
`STAGE32.md`'s own framing ("if you do any profiling to help fix the memory
issue... see if you can profile this at the same time"); given `REPORT26.md`
already investigated this question thoroughly and this stage's own
measurement is consistent with its conclusion, I don't think it needs
further dedicated investigation right now.

## Command line arguments

None. No `vibe_sat` CLI flags changed in either language this stage.

## Testing

- Go: `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./...`,
  `go test -race -count=10 ./internal/dfs/...` all clean.
- Rust: `cargo build --release`, `cargo clippy --all-targets -- -D
  warnings`, `cargo fmt --check`, `cargo test --release` (198 tests) all
  clean.
- Cross-language: `util/benchcompare` sweeps (above) — 0 mismatches, 0
  verification failures, both before and after the shed-frequency fix.
- Manual verification: `REPORT29.md`'s exact repro command against the
  same 415 MB file, memory monitored directly via `ps`, in both languages.

## Open questions / notes for you

- **The shed-check interval (`shedCheckInterval`/`SHED_CHECK_INTERVAL`,
  now ~1,000,000 nodes) is a conservative choice based on exactly one
  adverse measurement, not a principled optimum.** It's possible a smaller
  value would be safe on most instances and provide meaningfully better
  load balancing on large, genuinely imbalanced searches; it's also
  possible other instances are sensitive to it in ways this one measurement
  doesn't reveal. I erred toward "rarely shed" specifically because the
  downside I measured (a 15x+ regression) was so much larger than the
  upside I could measure (better balancing on the one large-file case this
  stage's memory fix targets, where memory safety no longer depends on
  shedding frequency at all). If parallel `dfs`'s load-balancing quality on
  large, genuinely lopsided search trees becomes a concern later, this
  constant — and possibly a smarter shed policy less sensitive to heuristic
  restart cost — would be the place to start.
- I did not investigate whether `SelectVarFast` (the non-heuristic variant)
  is similarly sensitive to shed frequency; its lack of both a weighted
  score and rng-driven tie-breaking makes it a plausible candidate for
  being immune to this specific mechanism, but I did not verify that.
- No language/toolchain version changes needed.
