# Report: Stage 38

## Summary

`STAGE38.md` asked for a survey: `REPORT23.md`'s "bigger possible
changes" item 2 (reduce allocation in `analyze`/`addLearnedClause`,
found responsible for ~91% of all heap allocation in an 8-thread
`cdcl` run) was left open pending item 3 (shorter learned clauses),
now done as Stage 36's clause minimization. With that done, is the
allocation-reduction fix still worth pursuing?

**No — the data says don't.** Three independent measurements, all
pointing the same direction:

1. **The allocation *share* barely moved.** `analyze`/`addLearnedClause`
   still account for ~88% of heap allocation (by bytes) at 8 threads —
   essentially `REPORT23.md`'s original ~91%, not meaningfully reduced
   by shorter clauses. At 32 threads it drops to ~67%, but only because
   clause-sharing overhead grows with thread count, not because these
   two functions got cheaper.
2. **But that allocation never cost meaningful CPU time, before or
   after.** A CPU profile of the same benchmark shows `propagate`/
   `chooseWatch` (the watched-literals scan itself) still dominate at
   95-96% of total CPU time at both 8 and 32 threads — essentially
   unchanged from `REPORT23.md`'s own original finding. `analyze`'s own
   cumulative CPU cost is under 3%; `addLearnedClause`'s is small
   enough to barely register.
3. **Direct confirmation: disabling Go's garbage collector entirely
   made the search *slower*, not faster.** `GOGC=off` vs. the default,
   5 repeats each, single-threaded (to remove multi-thread race
   variance): consistently ~6% *slower* (18.84s mean vs. 19.98s), while
   peak memory more than doubled (41.6 MB → 90.6 MB). If GC/allocation
   overhead were actually costing real wall-clock time, removing it
   entirely should help, not hurt.

Allocation *share* and allocation *cost* turned out to be two
different questions with two different answers — a pooled/arena
allocation scheme (or the harder refcounting/epoch-based reclamation
`REPORT23.md` said a naive pool would need) would very plausibly
introduce real engineering risk for a ceiling of maybe 2-3% wall-clock
time, at best, even eliminated perfectly. **No code changes this
stage** — the survey's own conclusion is that none are warranted.

## The survey

### Method

Reproduced `REPORT23.md`'s own profiling methodology exactly:
`internal/cdcl/bench_test.go`'s existing `BenchmarkRunHardParallel8`/
`BenchmarkRunHardParallel32` (unchanged since Stage 23 — the same hard
`uuf250-1065/uuf250-01.cnf` instance, same VSIDS/round-robin
configuration), `go test -bench=... -benchtime=1x -memprofile=...
-cpuprofile=...`, `go tool pprof`.

### Allocation share: barely changed

`go tool pprof -alloc_space -focus='analyze|addLearnedClause'`:

| | 8 threads | 32 threads |
|---|---|---|
| `REPORT23.md`'s original figure | ~91% | *(not separately reported)* |
| This stage, by bytes (`alloc_space`) | 87.71% | 66.64% |
| This stage, by count (`alloc_objects`) | 93.34% | *(not separately re-measured)* |

Stage 36's minimization shortens the *average* learned clause, but
`analyze` still builds one fresh slice via `append` and
`addLearnedClause` still stores it every single conflict, regardless
of how long the final clause turns out to be — the *number* of
allocation events per conflict didn't change, only their average size.
That's exactly why the share didn't move much: the dominant cost here
was never really "how big is each clause," it's "we allocate once per
conflict, and there are still tens of thousands of conflicts." The
drop at 32 threads is a dilution effect (clause-sharing's own
`exportBuffer.publish`/`importClause` allocation grows with thread
count), not evidence that `analyze`/`addLearnedClause` themselves got
cheaper.

### CPU cost: negligible, and unchanged from Stage 23

`go tool pprof -top` on the CPU profile, same two benchmarks:

| | 8 threads | 32 threads |
|---|---|---|
| `propagate` (cumulative) | 96.11% | 94.95% |
| `chooseWatch` (cumulative, inside `propagate`) | 45.58% | 45.65% |
| `analyze` (cumulative) | 2.46% | 2.94% |
| `addLearnedClause` (cumulative) | *(too small to list separately)* | 0.55% |
| `runtime.mallocgc`/`growslice` (combined) | ~0.6% | ~1.3% |

This matches `REPORT23.md`'s own original finding almost exactly ("all
of `propagate`'s cost is fundamentally the two-watched-literals
scheme's own cost — there's no single wasteful thing left to trim,"
its "bigger possible changes" item 3). Nothing about that has changed:
`analyze`/`addLearnedClause` were never the CPU bottleneck, only the
allocation-*share* leader — a real but different metric.

### The direct test: does removing GC entirely help?

The most convincing single experiment: if GC/allocation overhead were
actually costing wall-clock time, eliminating it outright should show
it. `GOGC=off` disables Go's garbage collector completely (the heap
only ever grows; nothing is ever reclaimed).

Run single-threaded (to avoid the 8-thread benchmark's own "first
solution wins the race" nondeterminism — this instance is UNSAT, so a
single-threaded proof is fully deterministic), same binary, 5
interleaved repeats each, on `uuf250-1065/uuf250-01.cnf`
(`--no-preprocessing --alg-params 2 2`):

| | wall time (5 runs) | mean |
|---|---|---|
| `GOGC=100` (default) | 18.51s, 18.78s, 19.06s, 19.10s, 18.75s | 18.84s |
| `GOGC=off` | 19.78s, 19.88s, 20.80s, 19.68s, 19.77s | 19.98s |

`GOGC=off` was slower in **every single one of the 5 pairs** — a
consistent, reproducible ~6% regression, not noise. Peak resident set
size (`/usr/bin/time -v`): 41,600 KB (default) vs. 90,624 KB
(`GOGC=off`) — more than double.

This makes sense once you think about *why*: with GC on, freed
allocations get reused, keeping the live working set compact and
cache-friendly; with GC off, every single allocation is genuinely new
memory, so the heap sprawls and cache locality gets worse as the run
progresses — the opposite of what "GC pressure is costing us time"
would predict. Go's GC is doing real, useful work here, not imposing a
tax.

### Answering your `sync.Pool` question directly

Is a `sync.Pool` viable here at all, independent of whether it's worth
doing? A real, informed answer, not just "it's unsafe":

`sync.Pool` needs a well-defined "this object is genuinely done, hand
it back" moment. For `cdcl`'s learned clauses, that moment **does**
exist and **is** alias-safe: `reduceClauseDatabase`'s eviction of a
clause. Once a clause is evicted (not locked as any variable's current
reason), nothing else in that thread holds a reference to its backing
array — clause-sharing imports always deep-copy
(`clauseshare.go`/`clause_share.rs`'s own documented "own copy; must
not alias the exporter's published slice"), so there's no cross-thread
aliasing hazard either. `REPORT23.md`'s "genuinely indefinite
lifetime" caution was right that you can't treat this like a scoped
scratch buffer with a symmetric get/put pattern, but the *eviction*
event specifically is a safe, well-defined recycling point — contrary
to how cautious `REPORT23.md`'s own phrasing reads on a second look.

**The reason it still wouldn't help is different, and more decisive:
`reduceClauseDatabase` only runs when `--alg-params val3` (a memory
limit) is set and exceeded.** By default, `memoryLimitBytes` is `nil`
— unbounded — meaning `reduceClauseDatabase` **never runs at all** in
`cdcl`'s default configuration, and so a pool fed only by its
evictions would never receive anything back to reuse for the vast
majority of real usage. Even with an explicit memory limit, evictions
happen far less often (only when the estimated database size crosses
the threshold) than allocations (every single conflict), so supply
would rarely meet demand even then. Combined with the CPU-cost finding
above, a `sync.Pool` here would be safe to build correctly, but nearly
useless in practice.

## Recommendation

**Don't pursue this.** `REPORT23.md`'s "bigger possible changes" item
3 (a genuinely different watch-list representation — the one thing
that actually dominates CPU time, then and now) remains the only
lever left with real upside, and it's already correctly flagged there
as "real research-grade solver engineering," well outside a survey
stage's scope. Revisit the allocation question only if a future
profile on a meaningfully different workload (a much larger real
instance, or a configuration that actually exercises
`reduceClauseDatabase` heavily) shows a different picture than this
one did.

## Verification

This stage made no code changes, so nothing to build/test/verify in
the usual sense. The measurements themselves were cross-checked two
ways per claim (allocation share by both `alloc_space` and
`alloc_objects`; the GC-cost question by both a CPU profile *and* a
direct `GOGC=off` wall-clock/memory experiment) before being trusted
enough to write down. Scratch profiling artifacts
(`go_src/cdcl.test`, `*.prof` files) were not committed.

## Documentation

Per the standing instruction, checked this stage:

- `docs/references.md`/`docs/usage.md`: no update needed — no new
  technique or CLI surface.
- `docs/background.md`: updated. The existing "every benchmark claim
  was checked against real measurement" bullet (under "Other things
  worth knowing") now includes this stage's finding alongside the
  other cases where measurement overturned an intuitive-looking guess.

## Questions for you

- Any interest in re-running this same survey against a much larger
  real instance (e.g. from `sat_comp/2018`) to see whether the
  conclusion changes at a different scale, or is this project's own
  standard benchmark instance representative enough to trust as-is?
- `REPORT23.md`'s item 3 (a genuinely different watch-list
  representation) is now the only concrete lever left for `cdcl`'s
  core-loop performance. Worth scoping as its own future stage, or
  staying on the back burner per the project's existing "real
  research-grade solver engineering" framing?
