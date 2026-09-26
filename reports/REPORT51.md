# Report: Stage 51

## Summary

`STAGE51.md` asked for `REPORT50.md`'s Tier 1 item 1: `chooseWatch`
remembering a scan position across calls (`REPORT41.md`/`REPORT47.md`),
the other real, standard MiniSat-lineage watch-scan optimization this
project hadn't tried yet, and the top candidate once `REPORT48.md`'s
profile named `chooseWatch` itself (not `propagate`'s own candidate
discovery) the dominant remaining cost.

**Result: a clear, substantial win, confirmed in both languages.**
`chooseWatch`'s own share of a fresh CPU profile dropped from 49.6%
(`REPORT48.md`'s number) to 23.5% cumulative; ~34% faster on the
standing hard benchmark instance in Go, ~31% in Rust; a 176-file
correctness/timing sweep found 0 verify failures, 0 cross-language
mismatches, and four previously-timed-out files now solved within the
same time budget. A 32-file multi-threaded sweep (exercising clause
import's own watch-selection path) was equally clean. One honest
caveat: a handful of individual files got measurably *slower* — an
expected consequence of a search-order change, not a correctness
concern, discussed below.

## The fix

`chooseWatch` scans a clause's literals looking for one that is
neither `avoid` (the clause's other current watch) nor currently
false, to become a replacement for whichever watch just went false.
Previously this scan always started at literal index 0. The fix: a new
per-clause `chooseWatchPos[c]`/`choose_watch_pos[c]` remembers where
the *previous* scan for that clause left off, and the next scan starts
there instead — wrapping around circularly (`idx = (start+i) % n`) so
every literal is still examined at most once regardless of `start`,
guaranteeing no valid candidate is ever missed just because of where
the scan happened to begin. On success, the position right after the
literal actually chosen (`(idx+1) % n`) is remembered for next time.

The motivating case: a long-lived clause whose early literals became
false long ago and have stayed that way (common — once a variable is
decided early and stays decided for a long stretch of the search, every
clause mentioning it has a permanently-false literal sitting at
whatever index it happens to occupy) would otherwise have that same
literal re-examined and rejected on *every single call* for as long as
it remains false, for as long as the clause's watches keep moving.
Starting from a rotating offset spreads that redundant work out instead
of concentrating it on the clause's own first few literals.

`chooseWatchPos` is purely a heuristic hint, never required for
correctness (any starting position works, by the wraparound guarantee
above), so it needed no special handling anywhere clauses are added or
removed: freshly learned or imported clauses start at position 0 (no
history yet), and `reduceClauseDatabase`/`reduce_clause_database`
simply rebuilds it as a fresh all-zero array for the surviving clauses
rather than remapping old positions through its existing
old-index-to-new-index table.

## Measured results

**Standing hard instance** (`benchmark/uuf250-1065/uuf250-01.cnf`), old
(current committed code) vs. new (with the fix), built from the same
source tree via `git stash`:

| Language | Old | New | Change |
|---|---:|---:|---:|
| Go (3 repeats) | 9.13s / 9.06s / 9.08s | 5.98s / 6.12s / 5.97s | **~34% faster** |
| Rust (2 repeats) | 8.20s / 8.26s | 5.60s / 5.83s | **~31% faster** |

Both UNSAT, matching the pre-change verdict, every time.

**CPU profile** (`BenchmarkRunHardSingleThreaded`, 5 reps, new code):

| Function | flat% | cum% |
|---|---:|---:|
| `propagate` | 51.66% | 93.90% |
| `chooseWatch` (inline) | 16.39% | **23.54%** |
| `cnf.Literal.Var` (inline) | 15.89% | 15.89% |
| `isTrue` (inline) | 5.47% | 16.81% |
| `isFalse` (inline) | 3.24% | 7.26% |

Down from `REPORT48.md`'s 49.59% cumulative — `chooseWatch` is no
longer close to half the total, though it remains the single largest
named cost after `propagate`'s own inline work (which grew in relative
share only because the other piece shrank so much, not because it got
slower).

**Broad correctness + timing validation via `util/benchcompare`**
(old Go binary vs. new Go binary, treated as the tool's two
"languages" exactly like `REPORT48.md`'s own methodology):

- 176 files across every random `benchmark/uf*`/`uuf*` subdirectory
  from 20 to 250 variables (`--sample=16`, `--time-limit-secs=15`): old
  172/176 solved (4 timeouts), new **176/176 solved (0 timeouts)** — a
  qualitative win, not just a speed one. 0 verify failures, **0
  cross-language mismatches**. The four newly-solved files:

  | File | Old | New |
  |---|---|---|
  | `uf250-07.cnf` | TIMEOUT | SAT, 12.37s |
  | `uuf250-051.cnf` | TIMEOUT | UNSAT, 13.36s |
  | `uuf250-058.cnf` | TIMEOUT | UNSAT, 11.47s |
  | `uuf250-092.cnf` | TIMEOUT | UNSAT, 8.31s |

  Restricting to the 26 files that took the old binary more than 0.3s
  (where the fix's effect is actually visible above run-to-run noise):
  total time across all 26 dropped from 142.0s to 100.2s (**~29.5%
  less**), mean 5.46s → 3.85s. Not every file moved in the same
  direction, though: 22 of 26 got faster (several by 40-88%), but 4 got
  slower, most notably `uf250-03.cnf` (0.94s → 4.67s). This is the same
  "search-trajectory divergence" phenomenon `REPORT45.md`/`REPORT46.md`
  already documented for the watch-list rewrite itself — changing which
  literal becomes a new watch changes propagation order, which for a
  chaotic CDCL search can occasionally send one specific instance down
  a meaningfully worse path even while the aggregate and the vast
  majority of individual files improve. Reported honestly, not
  smoothed over: the net effect is clearly positive, but not
  uniformly so.
- 32 files, multi-threaded (`--num-threads=4`, exercising clause
  import's own watch-selection path, `importClause`/`import_clause`):
  32/32 solved both old and new, 0 verify failures, **0 mismatches**,
  mean time 1.16s → 0.83s.

**Rust**: the fork that ported this also spot-checked Go vs. Rust
agreement on the standing hard instance (both UNSAT) as part of its own
verification; I independently re-timed both Rust binaries myself (see
table above) rather than relying solely on the fork's report.

## Verification

- **Go**: `go build ./...`, `go vet ./...`, `gofmt -l .` clean;
  `go test ./...` (all ten packages, every pre-existing test passing
  unchanged) and `go test -race -count=10 ./internal/cdcl/...` clean.
- **New Go tests** (`internal/cdcl/cdcl_test.go`,
  `internal/cdcl/watch_test.go`): `TestChooseWatchStartsFromGivenPosition`,
  `TestChooseWatchWrapsAroundFromGivenPosition`,
  `TestChooseWatchFailsWhenNoneAvailableFromGivenPosition` (direct,
  hand-traced tests of the new circular-scan logic), and
  `TestPropagatePersistsChooseWatchPos` — the wiring-proof test: a
  five-literal clause with `chooseWatchPos` manually set to 4 before a
  real `propagate()` call must land on literal 5, not the
  earlier-but-also-valid literal 3 a from-scratch scan would have
  found, proving `propagate` actually threads the remembered position
  through rather than just that `chooseWatch`'s own standalone logic is
  correct. Confirmed to actually fail (finds literal 3, and
  `chooseWatchPos` ends up 3, not 0) when the `propagate` call site is
  temporarily hardcoded back to `start=0`, before restoring the fix and
  confirming it passes.
- **Rust**: `cargo build --release`/`--all-targets`, `cargo fmt
  --check`, `cargo clippy --all-targets -- -D warnings` clean; `cargo
  test --release` — **266 passed, 0 failed** (was 262; four new tests
  mirroring Go's exactly, including the same wiring-proof test,
  independently teeth-checked the same way). `choose_watch` returns
  `Option<(Literal, usize)>` rather than a Go-style 3-tuple with an
  explicit `found` bool — the more idiomatic Rust shape for the same
  logic.
- Both binaries independently rebuilt and re-verified by me directly
  (not just trusting the porting fork's own report): build/vet/fmt/test
  suite re-run from scratch, diff read in full, and the standing hard
  instance re-timed myself in both languages.
- **Incidental cleanup**: a stray `go_src/cdcl.test` binary left behind
  by an earlier `go test -cpuprofile` invocation during this stage's
  own profiling was found and removed before finishing.

## Documentation

- `docs/background.md` — new "`chooseWatch`'s remembered scan
  position" subsection right after the existing blocking-literal-check
  writeup, covering the mechanism, the measured results, and the
  honest per-file-variance caveat.
- `docs/usage.md`/`docs/internal-parameters.md`/`docs/references.md`
  — no changes needed: no new CLI surface, no new tunable parameter
  (`chooseWatchPos` is unconditional internal state, not
  `--alg-params`- or `.vibe_sat.json`-selectable), no new literature
  citation (still the same MiniSat-lineage technique already covered
  by the existing Chaff citation).

## Questions for you

- The per-file variance (most files faster, a few meaningfully slower)
  is inherent to changing search order, not a bug — comfortable with
  that tradeoff given the strong aggregate result, or worth me digging
  into `uf250-03.cnf` specifically to understand why it regressed so
  much?
- `REPORT50.md`'s Tier 1 also listed bootstrap interruptibility and the
  progress meter, in that order — want me to continue straight into
  bootstrap interruptibility next, or is there anything about this
  stage's results you'd like to revisit first?
