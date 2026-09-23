# Report: Stage 46

## Summary

`STAGE46.md` asked for `REPORT45.md`'s flagged follow-up: `dfs` has
the identical occurrence-index-as-watch-list inefficiency `cdcl`'s
`propagate` had before `STAGE45.md`'s fix. Applied the same fix to
`dfs`'s BCP this stage, in both languages.

**Result: the fix is real and correctly implemented, but its payoff
is much smaller than `cdcl`'s** — a genuinely different outcome from
simply "the same fix again," and an honest one to report rather than
assume. On this project's own standing hard `dfs` benchmark instance,
wall-clock time dropped ~4% with `dfs`'s default weighted `SelectVar`
heuristic, rising to ~11% with the cheaper static-order heuristic. A
CPU profile explains the gap directly: `SelectVar`'s own per-node
clause rescan dominates at ~81-83% of CPU time on this instance,
leaving BCP only 8-10% of the total to begin with — nothing like
`cdcl`'s 97%+, where `propagate` really was almost the entire cost.
The same architectural fix helps in direct proportion to how much of
the total cost BCP actually is, which for `dfs`'s default
configuration was never as large a share as it was for `cdcl`.

One structural difference from `cdcl` mattered for correctness, not
just performance: `dfs`'s watch state is genuinely cloned in two real
places (`bfsSeed`'s per-worker seeds, `shedFrame`'s stolen snapshots),
so the new watcher lists needed a real deep copy on clone, not just a
copy of the outer slice — verified directly by deliberately weakening
the clone to a shallow copy and confirming a dedicated test catches
it.

Also, since `dfs` (unlike `cdcl`) had no other consumer of the old
occurrence index, this stage removed it from `dfs`'s public API
entirely, rather than leaving it threaded through unused.

## The fix

Identical in shape to `STAGE45.md`'s `cdcl` fix (see `reports/REPORT45.md`
for the full original diagnosis): a new `watchersPositive[v]`/
`watchersNegative[v]` pair inside `watchState`, holding only clauses
*currently* watching literal `+v`/`-v`, maintained incrementally as
watches move via the same MiniSat-style in-place compaction algorithm
(`scanned`/`keep` two-pointer scan over the falsified literal's own
watcher list — see `REPORT45.md` for the exact reasoning about why
the post-conflict tail must still be copied into the compacted
prefix). `BCP` no longer takes an `*occurrence.Lists` parameter at
all.

### A `dfs`-specific wrinkle `cdcl` didn't have: clone independence

`cdcl`'s solver is never cloned — one instance, mutated in place, for
the whole search. `dfs`'s `watchState`, since `STAGE32.md`'s
persistent-trail rewrite, is *mostly* also mutated in place for a
single sequential exploration, but is still genuinely cloned in two
real spots: `bfsSeed`'s BFS expansion (one clone per branch while
collecting the initial per-worker seeds) and `shedFrame` (a worker
occasionally materializing a snapshot of its own current state for
another idle worker to steal). `cloneWatchState`'s existing behavior
for `watch` itself (`[][2]cnf.Literal`, a slice of fixed-size arrays)
was already correct — copying the outer slice copies every value.
`watchersPositive`/`watchersNegative` are slices of *slices*
(`[][]int`), where the same shallow-copy approach would leave the
clone's and the original's inner `[]int` watcher lists sharing the
same backing array — a later in-place compaction write on either
branch's watcher list would then silently corrupt the other's, a
correctness bug that would only surface later as a wrong verdict on
some specific input, not a crash at the point of the mistake. Fixed
by deep-copying each inner slice independently.

**Verified directly, not just reasoned about**: a dedicated test
(`TestCloneWatchStateWatcherListsAreIndependent`) clones a two-clause
watch state, runs `BCP` on *only* the clone (one clause moves away,
the other stays and writes into the compacted list), then asserts the
*original*'s watcher lists are completely untouched. Deliberately
weakening the clone to a shallow copy and re-running this test
produced a real, specific failure (`watchersFor(+1) changed to [1 1]
after mutating only the clone, want unchanged [0 1]`) — confirming the
test has teeth, not just that it passes vacuously — before restoring
the real fix and confirming it passes again.

### Removing `lists` from `dfs`'s public API

Unlike `cdcl` (still needs `internal/occurrence.Lists` for
`rephaseFromWalkSAT`, `STAGE43.md`), a full grep confirmed `dfs`'s
occurrence index had exactly one consumer: BCP's old candidate
discovery. Once that stopped needing it, keeping `lists` threaded
through `Run`, `RunParallel`, `bfsSeed`, `startSearch`,
`newSearchState`, `searchState`, and the parallel worker config
struct — now doing nothing — would have been dead plumbing left in a
public API for no reason. Removed entirely instead, including
`cmd/vibe_sat/main.go`'s now-unnecessary `occurrence.Build(problem)`
call in `runDFS`, and every test call site across `dfs_test.go`/
`parallel_test.go`/`bench_test.go` (mechanical, ~46 call sites).

## Why the payoff differs so much from `cdcl`

A CPU profile of `BenchmarkRunHardSingleThreaded` (`internal/dfs`'s
own standing benchmark, `benchmark/uuf175-753/uuf175-083.cnf`) before
and after the fix, default `SelectVarWeighted`:

| Function | Before (cum.) | After (cum.) |
|---|---:|---:|
| `SelectVar` | 81.39% | 83.13% |
| `BCP` | 9.80% | 7.68% |

`SelectVar`'s own per-node cost (rescanning every not-yet-satisfied
clause, the STAGE5.md weighted heuristic's own documented trade-off)
was always the dominant cost on this instance, not BCP — the exact
opposite of `cdcl`'s profile, where `propagate`/`chooseWatch`
accounted for 95-98% of total time across three separate profiling
passes. Speeding up a function that was only ever ~8-10% of the total
can only move the needle so much. Switching to the cheaper
`SelectVarFast` heuristic (`--alg-params val1=1`, no clause scanning
at all) shifts the balance toward BCP mattering more, which is exactly
what the timing numbers below show.

## Measured results

Hard instance (`benchmark/uuf175-753/uuf175-083.cnf`, the same file
`internal/dfs/bench_test.go`'s own standing benchmark uses), old vs.
new Go binaries built from the same source tree (`git stash` to
isolate the change):

| Heuristic | Old | New | Change |
|---|---:|---:|---:|
| `SelectVarWeighted` (default), `go test -bench` (8 reps) | 2.121s/op | 2.037s/op | ~4% faster |
| `SelectVarFast` (`val1=1`), CLI wall-clock | 17.22s | 15.28s | ~11% faster |

Both UNSAT, matching the pre-change verdict, every time.

**Broad correctness validation via `util/benchcompare`**: 90 files
across every random/structured `benchmark/` subdirectory
(single-threaded, `--sample=6`, `--time-limit-secs=15`) — 78/90 (Go)
and 81/90 (Rust) solved, 0 verify failures, **0 cross-language
mismatches**. A second, multi-threaded sweep (24 files,
`--num-threads=4` — exercising `bfsSeed`'s and `shedFrame`'s real
clone paths, not just unit tests) — 18/24 (Go) and 23/24 (Rust)
solved, 0 verify failures, **0 cross-language mismatches**. Differing
solved counts between languages reflect legitimate search-trajectory
divergence (candidate-discovery order changed, so which branch a
timeout catches mid-search differs) — the same, already-documented
pattern `REPORT45.md` found for the identical `cdcl` change — not a
correctness problem: every file *both* languages reached a verdict on
agreed exactly.

## Verification

- **Go**: `go build ./...`, `go vet ./...`, `gofmt -l .` clean;
  `go test ./...` (all ten packages, every pre-existing test passing
  unchanged) and `go test -race -count=10 ./internal/dfs/...` clean.
- **New tests** (`internal/dfs/watch_extra_test.go`):
  `TestNewWatchStatePopulatesWatcherLists`, `TestBCPWatcherListCompaction`
  (the same hand-traced 4-clause gap/conflict/survivor scenario
  already verified for `cdcl`, reproduced here for `dfs`'s own `BCP`),
  and `TestCloneWatchStateWatcherListsAreIndependent` (see above).
- **Rust**: `cargo build --release`, `cargo build --all-targets`,
  `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`
  clean; `cargo test --release` — 257 passed, 0 failed (was 254; 3 new
  tests mirroring Go's exactly). One genuine Rust/Go asymmetry found
  along the way: Go's `cloneWatchState` needs a manual,
  element-by-element deep copy of its `[][]int` watcher-list fields to
  avoid backing-array aliasing between clone and original; Rust's
  `Vec<Vec<usize>>` has no such hazard at all — `Vec::clone` always
  allocates fresh storage, recursively, so `#[derive(Clone)]` on
  `WatchState` already produces a correct deep copy with no special
  handling needed. Confirmed by reasoning (there's no shallow-copy
  variant of `Vec<Vec<T>>` to accidentally introduce without changing
  the field types entirely), not by attempting to force a bug that
  the type system already rules out.
- **Broad correctness validation, both languages, both single- and
  multi-threaded**: single-threaded (90 files) — 79/90 (Go), 81/90
  (Rust) solved, 0 verify failures, 0 mismatches; multi-threaded (24
  files, `--num-threads=4`) — 18/24 (Go), 23/24 (Rust) solved, 0
  verify failures, 0 mismatches.
- **Rust timing** (`uuf175-083.cnf`, old vs. new, 2 repeats): default
  `Weighted` heuristic 1.453s → 1.412s (~2.8% faster, matching Go's
  modest ~4%); cheap `Fast` heuristic 15.03s → 13.71s (~8.8% faster,
  matching Go's larger ~11%) — the same "improvement scales with
  BCP's actual share of total cost" pattern holds in both languages.

## Documentation

- `docs/background.md` — the "watched literals" paragraph under
  DFS/DPLL rewritten to describe the applied fix (previously described
  it as an open opportunity), including the measured payoff and why
  it differs from `cdcl`'s, and the clone-independence wrinkle; a new
  bullet under "Go versus Rust: what this project actually found"
  noting that Rust's `Vec<Vec<T>>` made the shallow-copy failure mode
  structurally impossible, where Go needed a manual deep copy and a
  dedicated test to catch getting it wrong.
- `docs/usage.md`/`docs/internal-parameters.md`/`docs/references.md`
  — no changes needed: no new CLI surface, no new tunable parameter,
  no new literature citation.

## Questions for you

- `REPORT45.md`'s other open question — a broader benchmark sweep
  against `minisat`/`cryptominisat5` re-running something like
  `REPORT40.md`'s own comparison — is still open (you deferred it in
  favor of this `dfs` fix). Worth doing now that both `cdcl` and `dfs`
  have the fix, or still lower priority than whatever's next on
  `low_priority.md`?
- Given how much smaller `dfs`'s payoff was, and that `SelectVar`
  (not BCP) is `dfs`'s own actual bottleneck on its default
  configuration — is a future look at `SelectVar`'s own cost (it
  rescans every not-yet-satisfied clause on every node, by design)
  worth scoping, or is `dfs` performance simply not a current
  priority the way closing the `minisat` gap on `cdcl` was?
