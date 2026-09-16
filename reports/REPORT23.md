# Report: Stage 23

## Summary

Stage 23 profiled `cdcl` in both languages on this project's own
hardest tractable benchmark instance (`uuf250-1065/uuf250-01.cnf`;
see `reports/REPORT21.md`), single-threaded and multithreaded, with
specific attention to the shared clause-database's contention cost
per `STAGE23.md`'s explicit request. One small, safe, verified-
positive fix was applied to both languages (a redundant comparison
removed from `propagate`'s inner loop: ~1.6% faster in Go, ~3.4%
faster in Rust, measured rigorously -- see below). One candidate
fix that looked obviously correct on paper (the classic MiniSat
"blocking literal" optimization) was tried, measured, and found to
make things reproducibly *worse* on this project's own benchmark
set -- reverted, with the reasoning kept in this report and a code
comment rather than just silently discarded. The clause-database
contention question has a clean, direct, positive answer: it isn't
one. `maybeImport`/`maybe_import` costs on the order of 0.1-0.5% of
total CPU time even oversubscribed to 32 threads on a 10-core
machine -- the Stage 20 lock-free design is working exactly as
intended under real, sustained pressure, not just the isolated
stress test that first validated it.

## Profiling methodology

**Go**: `runtime/pprof` via ordinary Go benchmarks
(`go_src/internal/cdcl/bench_test.go`, new this stage, kept as
permanent infrastructure -- these are skipped by a plain `go test`,
only running with an explicit `-bench` flag). `go test -bench=...
-benchtime=1x -cpuprofile=... -memprofile=...` plus `go tool pprof`
gave real call-graph-level CPU and allocation profiles with no new
dependencies.

**Rust**: no equivalent was available in this sandbox. `perf` isn't
installed, and the packages that exist for it target kernel versions
this environment's WSL2 kernel doesn't match; `perf_event_paranoid=2`
would likely block it even if installed. Rather than add a profiling
crate as a new permanent dependency of `rust_src` for a one-off
diagnostic, I added temporary `Instant`-based timing instrumentation
directly to `run_loop` (measuring time spent inside `propagate`
specifically), ran it once, recorded the number, and reverted the
instrumentation immediately afterward -- the same add/measure/revert
pattern already used for debugging in Stage 18/21. This gives a
coarser picture than Go's full call-graph profile (one number, not a
breakdown), but it was enough to confirm the same hot spot dominates
in both languages before deciding where to apply a fix.

**A methodology note that mattered**: this environment's wall-clock
timing has real, non-trivial run-to-run noise -- `go test -bench`'s
own single-shot timing varied by several seconds run to run on an
otherwise fully deterministic computation (same seed, same VSIDS
heuristic, no randomness in the decision path at all). Every
comparison below instead uses the release CLI binaries directly, 5
interleaved repeats per configuration (A, B, A, B, ... rather than
5xA-then-5xB, to spread out any slow drift), comparing means. This
caught a real mistake early: an initial single-shot comparison made
the "blocking literal" change look like it might be noise-level
neutral; the rigorous interleaved comparison confirmed it as a clear,
consistent, ~25-28% regression instead (see below).

## Single-threaded hot spots

CPU profile on `uuf250-01.cnf`, single-threaded, before any change:

```
flat  flat%   sum%        cum   cum%
14.41s 53.21% 53.21%     26.62s 98.30%  (*solver).propagate
 9.52s 35.16% 88.37%     12.11s 44.72%  chooseWatch (inline)
 1.57s  5.80% 94.17%      1.57s  5.80%  cnf.Literal.Var (inline)
 1.11s  4.10% 98.26%      2.66s  9.82%  isFalse (inline)
 0.15s  0.55% 98.82%      0.22s  0.81%  (*solver).analyze
```

**98.3% of all CPU time is inside `propagate`**, and `chooseWatch`'s
linear scan for a replacement watch alone is 44.7% of it. This is
not a surprise in isolation -- `propagate` is the innermost loop of
the whole algorithm, run once per assignment on the trail, for every
clause watching whichever literal just became false -- but it does
mean essentially the *entire* algorithm's performance is this one
function's performance. `analyze`, restart bookkeeping, and
everything else together account for under 2%. The temporary Rust
instrumentation confirmed the identical shape: 98.6% of wall-clock
time inside `propagate` there too, unsurprising since both languages
implement the same two-watched-literal design.

### Fix applied: remove a redundant re-comparison (kept)

`propagate`'s loop determines which of a clause's two watch slots
holds the literal that just became false, to know which slot to
overwrite if a replacement is found:

```go
switch {
case watch[0] == falsifiedLiteral:
    otherWatch = watch[1]
case watch[1] == falsifiedLiteral:
    otherWatch = watch[0]
...
}
if replacement, found := chooseWatch(...); found {
    if watch[0] == falsifiedLiteral {   // <-- redundant: already known above
        watch[0] = replacement
    } else {
        watch[1] = replacement
    }
```

The second `watch[0] == falsifiedLiteral` check re-derives something
the `switch` above already determined. Remembering which slot
matched (`falsifiedSlot`) instead of re-comparing is a pure,
behavior-preserving simplification -- same decisions, same conflicts,
same everything except one fewer comparison per successful
replacement, applied identically in both languages.

Measured (5 interleaved repeats, CLI binary, `uuf250-01.cnf`,
single-threaded):

| | Go | Rust |
|---|---|---|
| baseline mean | 18.71s | 17.25s |
| with fix mean | 18.41s | 16.67s |
| improvement | ~1.6% | ~3.4% |

Small, but real, consistent (5/5 paired comparisons favored the fix
in both languages), and free of any tradeoff -- kept in both
languages. All existing unit tests, `go vet`, `gofmt`, `cargo fmt`,
and `cargo clippy` stayed clean; `go test -race -count=10` on `cdcl`
stayed clean.

### Fix tried and rejected: the "blocking literal" check

MiniSat-lineage solvers check whether the clause's *other* watched
literal is already true, before ever scanning for a replacement --
a clause satisfied via a true literal needs no attention at all until
some future backtrack changes that, so the scan can be skipped
outright. This looked like the obvious next win, given `chooseWatch`
was 44.7% of total time on its own. I implemented it (`if
s.x.LiteralIsTrue(otherWatch) { continue }` before the `chooseWatch`
call), and it measured as a clear, reproducible **regression**: 5/5
interleaved repeats consistently worse, ~18.0s (without) vs. ~23.1s
(with) -- a 25-28% slowdown, not the hoped-for improvement.

The likely reason, checked against the profile rather than just
guessed at: this project's benchmark clauses are almost entirely
length 3 (uniform random 3-SAT). `chooseWatch`'s scan, given it
already excludes `otherWatch` from consideration, has only **one**
other literal left to check in a 3-literal clause -- there is
essentially nothing expensive to skip. The added check, though,
costs a real branch and an extra `Literal.Var()` call on *every*
candidate, on *every* call, whether or not the case it exists to
handle (`otherWatch` already true) ever actually occurs. Reverted;
kept in this report and in a code comment at the `propagate` call
site in both languages, rather than silently discarded, since it's a
real, literature-standard technique that plausibly *would* help on
different (longer-clause) benchmarks -- see "Bigger possible changes"
below.

## Clause-database contention: the specific question `STAGE23.md` asked

Profiled at 8 threads (this machine's core count) and 32 threads
(deliberately oversubscribed, to make any real contention as visible
as possible) on the same instance.

| | 8 threads | 32 threads |
|---|---|---|
| wall clock | 5.56s | 6.93s (oversubscription regression, consistent with `REPORT21.md`) |
| total CPU-seconds across all workers | 44.20s | 68.88s |
| `maybeImport`'s own cost | 0.04s (0.09% of total) | 0.36s (0.52% of total) |
| top-25 profile nodes | `propagate`/`chooseWatch`/`isFalse`/`Var` -- same shape as single-threaded | same |

`maybeImport` (the periodic "drain other threads' export buffers"
check) doesn't even appear in the top 25 CPU consumers at 8 threads;
finding its actual cost required `go tool pprof -list` targeted at
`learnAndBackjump` specifically. At 32 threads it's still under 1%,
scaling roughly linearly with peer count (32 peers vs. 8 -- a ~4x
peer-count increase produced a ~6x cost increase, close enough to
linear that there's no sign of the *worse-than-linear* scaling real
lock contention would produce). A memory-allocation profile at 8
threads tells the same story: `importClause`'s allocations are 6.7%
of total heap traffic (23.35MB of 349.56MB) -- real, but a minority
contributor, well behind each thread's own ordinary learned-clause
growth (`addLearnedClause`, 69%) and conflict analysis (`analyze`,
22%).

**Conclusion: there is no clause-database contention problem to fix
here.** The Stage 20 prototype validated the lock-free ring-buffer
design in isolation; this stage confirms it holds up under real,
sustained multithreaded search pressure, not just a synthetic stress
test. `STAGE23.md` anticipated "any fix to this will most likely
require a bigger fix in a later stage" -- that turned out not to be
needed, because there's nothing here to fix.

## Bigger possible changes (not implemented -- for you to choose from)

1. **Conditional blocking-literal check, gated by clause length.**
   The rejected optimization above is a real, standard technique that
   plausibly helps on longer clauses (industrial/structured instances
   with wide clauses, or long *learned* clauses even within a random-
   3-SAT run) even though it hurts on this project's mostly-length-3
   original clauses. Implementing it as `if clause.len() > N &&
   is_true(other_watch) { skip }` (some threshold, empirically tuned)
   would need real benchmarking on the `blocksworld`/`flat`/`ssa`
   structured sets added in Stage 21 to justify a specific threshold
   -- a real, if modest, follow-up project, not a quick fix.
2. **Reduce allocation in `analyze`/`addLearnedClause`.** The memory
   profile above shows these two functions account for ~91% of all
   heap allocation in an 8-thread run, entirely independent of
   clause-sharing. `analyze`'s returned `learned` clause is built
   fresh via repeated `append` on every single conflict, then stored
   permanently as part of the clause database -- a pooled/arena
   allocation scheme could plausibly cut a meaningful fraction of GC
   pressure, but needs care: the learned clause's lifetime is
   genuinely indefinite (until `reduceClauseDatabase` evicts it), so
   a naive object pool would need real reference-counting or epoch-
   based reclamation, not a simple reuse-on-return pattern. A bigger
   design exercise than this stage's scope.
3. **A genuinely different watch-list representation.** All of
   `propagate`'s cost is fundamentally the two-watched-literals
   scheme's own cost -- there's no single wasteful thing left to trim
   at the margins (this stage's one applied fix was the last "free"
   simplification I could find). Going further would mean a
   different algorithmic approach entirely (e.g. a lazier watch
   update scheme, or SIMD-friendly clause layout for the scan itself)
   -- real research-grade solver engineering, not a Stage 23 change.

## Verification

- **Unit tests**: no test assertions changed; both languages' full
  suites pass unchanged (Go: 9 packages, `cdcl` 48 tests; Rust: 190
  tests). `go vet`, `gofmt -l .`, `cargo fmt --check`, `cargo clippy
  --all-targets -- -D warnings` all clean.
- **Race detector**: `go test -race -count=10 ./internal/cdcl/...`
  clean (the fix touches only single-thread-local state within one
  worker's own `propagate` call; nothing about its concurrency
  properties changed).
- **Cross-language correctness spot check**: both binaries agree with
  each other and with SATLIB's `uf`=SAT/`uuf`=UNSAT ground truth on a
  handful of representative files (`uf50-218`, `uuf50-218`,
  `uf175-753`, `uuf175-753`) single-threaded, and on `uuf175-01.cnf`
  at `--num-threads=4` (round-robin restart + clause sharing both
  active) -- unsurprising, since the applied fix is a pure,
  behavior-preserving simplification, but confirmed rather than
  assumed.
- **Performance**: see the tables above; all numbers are 5-repeat
  interleaved means on the actual CLI release binaries, chosen
  specifically because single-shot `go test -bench` timings on this
  environment turned out to have too much noise to trust directly.

## Testing

- Go: `go build ./...`, `gofmt -l .`, `go vet ./...`, `go test ./...`
  (9 packages), `go test -race -count=10 ./internal/cdcl/...` all
  clean. New: `go_src/internal/cdcl/bench_test.go` (permanent
  benchmark infrastructure, not exercised by a plain `go test`).
- Rust: `cargo fmt --check`, `cargo clippy --all-targets -- -D
  warnings`, `cargo test` (190 tests) all clean.
- Manual cross-language and performance verification as described
  above.

## Open questions / notes for you

- **The clause-database contention question has a clean negative
  answer**: there's nothing to fix. I'd treat `reports/REPORT22.md`'s
  item 4 (clause-database contention) as closed by this stage's
  measurement, not deferred further.
- **This stage's own benchmark harness** (`bench_test.go`) is a small,
  incidental step toward `REPORT22.md`'s item 18 (a standing Go-vs-
  Rust benchmark harness) -- it only covers `cdcl` so far, and only in
  Go (Rust has no equivalent kept around, since the timing
  instrumentation used here was deliberately temporary). Worth
  extending later if that item gets prioritized, not something I'd
  call "done" on the strength of this stage alone.
- **Item 1 of "bigger possible changes"** (conditional blocking-
  literal check) is the one I'd actually recommend picking up first
  if you want to keep pulling on this thread -- it's the only one of
  the three with a plausible, specific, testable payoff (structured/
  industrial instances) rather than a large open-ended design
  exercise.
- Preprocessing (item 2 from `REPORT22.md`) and the `dfs` deque
  rewrite (item 3) are both still open and untouched this stage --
  this stage was scoped to `cdcl` specifically, per `STAGE23.md`.
