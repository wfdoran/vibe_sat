# Report: Stage 37

## Summary

`STAGE37.md` asked for a smoke test in CI: a handful of small
`benchmark/` files run through the actual built CLI (both languages,
several flag combinations), asserting a clean exit and the expected
verdict — closing a real gap none of this project's existing tests
cover (nothing before this stage ever actually invoked the compiled
binary; `go test -race ./...` and `cargo test --release` check code
paths, not the CLI itself).

**No new verification utility was built.** You raised the possibility
of needing "a simple utility to independently check if a reported
solution is actually a solution" — `util/benchcompare` already *is*
that utility: it has run both binaries, checked SAT/UNSAT/UNKNOWN
agreement, and independently verified every reported solution against
a from-scratch DIMACS parser (`cnf.go`/`verify.go`, sharing no code
with either solver) since Stage 22. The only real gap was that
`benchcompare` never actually *failed* on a bad result — it printed a
report and always exited `0`, useful for a human reading output
interactively but useless as a CI gate. Fixed with one new flag,
`--fail-on-issues`, then the smoke test is just ten curated
invocations of the tool that already existed.

## Design

### Coverage, not a cross product

`STAGE37.md` asked for "a small number of runs" that together test "as
many parameter combinations as possible" — an each-value-at-least-once
design, not a full cross product (which would be enormous: 4
algorithms × 4 thread settings × 2 preprocessing states × up to 24
`cdcl` `--alg-params` combinations). Ten `benchcompare` invocations
(each one exercising both languages at once, since that's what the
tool already does) collectively cover every individual value called
for:

| # | `--algorithm` | `--alg-params` | `--num-threads` | preprocessing | file set |
|---|---|---|---|---|---|
| 1 | `hc` | `5000` | (none) | on | `uf20-91` (SAT) |
| 2 | `ws` | `1000` | 2 | on | `uf20-91` |
| 3 | `dfs` | `0` | (none) | on | `uf20-91` |
| 4 | `dfs` | `1` | 3 | off | `uuf50-218` (UNSAT) |
| 5 | `cdcl` | `0 0` | (none) | on | `uf20-91` |
| 6 | `cdcl` | `1 1` | 4 | off | `uuf50-218` |
| 7 | `cdcl` | `2 2` | (none) | on | `uf20-91` |
| 8 | `cdcl` | `3 3` | 2 | on | `uuf50-218` |
| 9 | `cdcl` | `2 4` | 3 | on | `uf20-91` |
| 10 | `cdcl` | `2 5` | (none) | on | `uuf50-218` |

Every `--algorithm` value, every `--num-threads` setting (none/2/3/4),
both preprocessing states, `dfs`'s both `val1` values, `cdcl`'s all
four `val1` values, and `cdcl`'s all six `val2` (restart strategy)
values each appear at least once — including `val2=4` (round-robin,
meaningful only with `--num-threads > 1`) deliberately paired with
`--num-threads=3` in row 9.

`uf20-91` (SATLIB's guaranteed-satisfiable 20-variable set) and
`uuf50-218` (its guaranteed-unsatisfiable 50-variable counterpart) are
both already in `benchmark/`; `--sample=1 --seed=1` picks one specific
file from each deterministically. Both are small enough that every row
above finishes in single-digit milliseconds per binary — genuinely
negligible added CI time.

`hc`/`ws` get a generously large restart/try count (`5000`/`1000`)
rather than a `--time-limit-secs`, per your own framing ("large number
of starts to virtually guarantee success"): a fixed count is
deterministic and hardware-independent, where a time limit would make
the smoke test's reliability depend on how fast the CI runner happens
to be that day.

### Making `benchcompare` a real CI gate: `--fail-on-issues`

Before this stage, `runMain` always returned `nil` (exit `0`) once a
sweep finished, regardless of what it found — appropriate for
`benchcompare`'s original, primary use (a human reading a report on a
big, possibly-hard benchmark sweep, where an occasional `UNKNOWN` from
a time limit is normal, not a bug), but useless for gating a build.

The new `--fail-on-issues` flag makes `runMain` return an error (exit
`1`) if the completed sweep's `summary` contains anything
`printReport`'s own "flagged runs" section would already point a human
at — a cross-language mismatch, a verification failure, or a crash —
**plus one case that wasn't tracked at all before this stage**: a
well-formed `UNKNOWN` verdict. `languageStats` had no field for it
(the `verdictUNKNOWN`/`verdictNone` distinction existed, but
`languageStatsOf`'s switch simply had no case for `UNKNOWN`, so it was
silently uncounted). Added `languageStats.Unknown`, tracked it, and
gave it its own column in the printed summary table — genuinely useful
information on its own, not just plumbing for the new flag. For a
smoke test's tiny, trivially-easy files, `UNKNOWN` from `hc`/`ws`
giving up is exactly as much a failure as a crash would be, even
though it's an unremarkable, expected outcome for `benchcompare`'s
usual big-sweep use on genuinely hard instances — hence an opt-in flag
rather than a change to the default behavior.

### The CI job

A new `smoke` job in `.github/workflows/ci.yml`, alongside the
existing `go`/`rust` jobs (Stage 28): `needs: [go, rust]`, so it
doesn't spend Actions minutes building both binaries and running the
sweep if the basic build/vet/test is already known broken. Builds both
binaries once (with the same cargo cache the `rust` job already uses,
so a cold Rust build doesn't need to happen twice per run), then runs
the ten `benchcompare` invocations above with `--go-bin`/`--rust-bin`
pointed at them (avoiding ten redundant from-source rebuilds) and
`--fail-on-issues` set.

## Verification

- `go build ./...`, `go vet ./...`, `gofmt -l .` clean for
  `util/benchcompare`.
- `go test ./...` passes (all of `benchcompare`'s existing tests, plus
  the new ones below).
- **New unit tests**: `TestHasIssues` (one case each for clean,
  mismatch, verification failure, crash, and the new `UNKNOWN` case —
  the last one specifically pinning `--fail-on-issues`'s own
  motivating scenario), and `TestSummarizeCountsAndMismatches` extended
  to also assert the new `Unknown` count on both languages.
- `TestPrintReportCleanSweep`/`TestPrintReportFlagsProblems` (existing
  tests) still pass unchanged against the new table column, confirming
  the format change didn't silently break either.
- **The actual ten CI commands, run locally** against freshly built Go
  and Rust binaries before trusting the workflow file: all ten exit
  `0`, all report 0 verify failures / 0 unknowns / 0 no-verdicts / 0
  cross-language mismatches, each in single-digit-to-low-double-digit
  milliseconds. (GitHub Actions itself isn't runnable locally, so this
  is the closest direct confirmation available that the workflow will
  behave as intended once pushed; the YAML was also checked with
  `python3 -c "import yaml; yaml.safe_load(...)"` for basic syntax
  validity.)
- No `go_src`/`rust_src` changes were needed this stage at all — the
  smoke test exercises the CLI as an external, unprivileged user would,
  which is exactly the point.

## Documentation

Per the standing instruction, checked this stage:

- `docs/references.md`: no update needed — no new literature.
- `docs/usage.md`: no update needed — no new `vibe_sat` CLI flags
  (`--fail-on-issues` is a `util/benchcompare` flag, a separate tool
  `usage.md` doesn't cover).
- `docs/background.md`: updated. A new bullet under "Testing, at every
  stage" explains why a CI smoke test closes a real gap the rest of
  that list doesn't.
- `util/benchcompare/README.md`: updated — a note on its new CI role,
  and the `--fail-on-issues` flag added to the flag table.

## Questions for you

- The ten-row coverage table hits every individual parameter value
  once, but never two "interesting" values together (e.g., `val2=5`
  Glucose restarts under `--num-threads > 1`, or `--no-preprocessing`
  with `hc`/`ws`). Worth widening later, or is each-value-once the
  right level of smoke-test coverage indefinitely?
- `benchmark/uf20-91`/`uuf50-218` were chosen for being tiny and
  already guaranteed-SAT/UNSAT by SATLIB's own naming. Any preference
  for different files, or is this fine?
