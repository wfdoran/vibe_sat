# Report: Stage 58

## What this stage was

`STAGE58.md` has no new feature. It asks four things about earlier reports:

1. REPORT52's `preprocess.Run` gap: is it worth its own stage?
2. REPORT56's type-error text gap: acknowledged as ok.
3. REPORT56's integral-float gap: acknowledged as ok.
4. REPORT56's overflow gap: "Is there any realistic chance that we would ever
   hit these bounds?"
5. "Only the first violation is reported": acknowledged as ok.

Go and Rust behave identically. Nothing has been committed.

## 1. The `preprocess.Run` gap

Stage 57 already did it: preprocessing now has a deadline of 1/10 of
`--time-limit-secs`, threaded through unit propagation, pure literals,
subsumption and BVE (`reports/REPORT57.md`). So no new stage is needed for
that. REPORT57 does leave one related gap open: the search's own bootstrap
calls the same quadratic `UnitPropagate`, so a long implication chain is still
slow there and a preprocessing deadline cannot help.

## 2. Is an overflow realistic?

**Yes, through one specific habit: typing INT64_MAX (9223372036854775807) to
mean "unlimited".** I measured it rather than guessing.

Only five parameters are multiplied by something large:

| Parameter | Multiplied by |
|---|---|
| `preprocess.bveWorkBudgetFactor`, `subsumptionWorkBudgetFactor` | clause count |
| `cdcl.minimizeWorkBudgetFactor` | learned-clause length x (bits + 1) |
| `cdcl.lubyBaseConflicts` | a Luby term (a power of two) |
| `cdcl.polynomialBaseConflicts` | the square of the restart count |

(`geometricBaseConflicts` already saturates since Stage 56; the others are
only compared, never multiplied.)

**Go, before this stage** (Stage 57 binary, INT64_MAX for each factor in turn):

| Instance (clauses) | default | `bveWorkBudgetFactor = INT64_MAX` |
|---|---|---|
| `uf100-0100` (430, even) | eliminated 2 | **eliminated 0** |
| `bw_large.b` (13772, even) | eliminated 9 | **eliminated 0** |
| `bw_large.a` (4675, odd) | eliminated 6 | eliminated 6 |
| `anomaly` (261, odd) | eliminated 5 | eliminated 5 |

The result depends on the clause count's parity: `INT64_MAX * n` wraps to
`2^63 - n` (positive, accidentally fine) for odd n, but to `-n` (negative, so
the budget is exhausted at once) for even n. So "unlimited" silently meant
"off" on half of all formulas. `subsumptionWorkBudgetFactor = INT64_MAX` also
changed the result on `anomaly` (subsumed 54 became 52, with
`clauses 261->200` instead of `198`) while leaving the other instances alone.

**Rust:** the loader reads numbers as f64, so the INT64_MAX literal becomes
2^63 and is rejected with `must be an integer`. That is not an overflow, but
the message is misleading. A smaller huge value such as `1e18` is accepted in
Rust and would wrap in a release build, as in Go. I did not run that case.

I did not construct a case where the restart-base or minimize products wrap on
a real run; they need much larger values or counts, and I only checked them by
arithmetic (below).

## 3. The fix: an upper cap of 1,000,000,000

I took the upper-cap option your REPORT56 question offered. Those five
parameters now accept only `0..1000000000` (`1..1000000000` for the two restart
bases). Out-of-range values, including INT64_MAX, are rejected with the
existing error style:

```
invalid over.json: cdcl.lubyBaseConflicts must be between 1 and 1000000000 (got 1000001000)
```

Why 10^9 (a judgement call, easy to change: `MaxScaleFactor` in
`go_src/internal/params/params.go`, `MAX_SCALE_FACTOR` in
`rust_src/src/params.rs`):

- The largest default is 18,000, so the cap is over 4 orders of magnitude above
  anything shipped, and at 10^9 a bound is "never binds" in practice (a BVE
  budget of 10^9 x clauses is far beyond any run).
- Overflow check, by arithmetic, at the cap: budgets overflow `int64` only past
  about 9.2 billion clauses; `minimize` only past a learned clause of about
  3.5 x 10^8 literals; the Luby base only past about 8 x 10^9 restarts; the
  polynomial base only past about 96,000 restarts, which at 10^9 conflicts each
  is over 10^14 conflicts. None of these can happen on a formula that fits in
  memory. I did not test any of those extremes.

I chose a cap over saturating multiplication because it is visible (an error,
not a silent clamp), mirrors the existing Stage 56 style, and needs no changes
to the solver code.

Changes: the validation in `params.go` and `params.rs`; both `--help`
paragraphs (the five ranges and one sentence explaining the cap, and that you
can use the cap itself for "unlimited"); mirrored test cases.

## 4. Verification

- Tests: the existing range tables got new rows in both languages (for each of
  the five parameters: 1,000,000,001 and INT64_MAX are rejected; 1,000,000,000
  is accepted). No new test functions, so Rust's total is still 310.
- Go: `gofmt -l` clean, `go vet ./...` clean, `go test ./...` passes,
  `util/paramtune` vet and tests pass.
- Rust: `cargo fmt --check`, `cargo clippy --release --all-targets -- -D warnings`
  clean; `cargo test --release`: 310 passed.
- End to end (final binaries): a file with all five at 1,000,000,000 runs
  `uf100-0100` identically in both languages (`eliminated=2`, SAT). 1,000,001,000
  is rejected with byte-identical text in both. INT64_MAX is rejected in both,
  but with different text (below).
- I did not rerun the Stage 55 fuzz or the benchmark checker: this stage only
  changes configuration validation, and the default values are unchanged.

## 5. Known gaps and notes

- **INT64_MAX message differs.** Go says
  `must be between 0 and 1000000000 (got 9223372036854775807)`; Rust says
  `must be an integer`, because its f64 JSON reader cannot represent the
  literal. Both are errors. Fixing it would mean an integer-aware JSON number
  reader in Rust (or saturating, which would also change what Rust accepts for
  uncapped fields). I left it; the Go message is the clearer one.
- `docs/internal-parameters.md` does not list the new range. I may not edit
  `docs/`; you may want to add it (and the other ranges, as noted in REPORT56).
- The restart bases and `minimizeWorkBudgetFactor` caps are checked only by
  arithmetic, not by a run that actually approaches the limit.
- Items 2, 3 and 5 of `STAGE58.md` are acknowledged as ok; no action.
