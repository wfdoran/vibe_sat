# Report: Stage 56

## What this stage was

From `STAGE56.md`, using your answers:

1. **Range-validate the internal parameters** (`.vibe_sat.json` /
   `--internal-params`): an unnatural value is now an error, and the CLI help
   lists every parameter's range.
2. **Geometric restart threshold:** Go now saturates at `INT64_MAX` like Rust.
3. **Docs:** `docs/internal-parameters.md` says `glucoseWindowSize` must be at
   least 1 (the one `docs/` edit you authorized).
4. REPORT55's questions: noted, no action (see section 5).

Go and Rust behave identically. Nothing has been committed.

## 1. Parameter validation

After loading (defaults, overlaid by the file), every field is range-checked.
The error names the field, the accepted range and the value, e.g.

```
invalid p1.json: cdcl.varActivityDecay must be greater than 0 and at most 1 (got 2)
```

Only the first violation, in field order, is reported. I ran both final
binaries on the cases below and the messages match byte for byte for every
range error.

| Field | Accepted |
|---|---|
| `cdcl.lubyBaseConflicts`, `polynomialBaseConflicts`, `geometricBaseConflicts` | integer >= 1 |
| `cdcl.geometricGrowthFactor` | finite, >= 1 |
| `cdcl.lrbAlpha`, `clauseActivityDecay`, `varActivityDecay` | > 0 and <= 1 |
| `cdcl.glueClauseLBDThreshold` | integer >= 0 |
| `cdcl.glucoseWindowSize` | integer, 1 to 10,000,000 |
| `cdcl.glucoseK` | finite, > 0 |
| `cdcl.minimizeWorkBudgetFactor` | integer >= 0 |
| `cdcl.rephaseIntervalRestarts`, `rephaseMaxFlips` | integer >= 1 |
| `preprocess.subsumptionWorkBudgetFactor`, `bveWorkBudgetFactor` | integer >= 0 |

How I chose them (I measured rather than assumed, on `uf250-0100` and your
`varActivityDecay` example):

- **Zero is allowed** for the three work-budget factors and the glue
  threshold: it is a defined, working setting (it turns that bound or technique
  off), so rejecting it would remove a legitimate option.
- **`glucoseWindowSize` has an upper bound** because the window is allocated up
  front. At 1e11 Go dies with "out of memory" and Rust with "memory allocation
  of 800000000000 bytes failed"; at 1e8 it works. 10,000,000 (80 MB) is a
  generous cap, but it is a judgement call.
- Most other extreme values (huge factors, tiny decays, etc.) did **not**
  crash on this easy instance; they were rejected on principle (outside the
  parameter's meaning), not because I saw each one fail. The probe instance
  was too easy to show quality degradation, so I cannot claim the ranges are
  tight in a performance sense.
- NaN and infinity cannot pass any float check (tested).

**Rust loader parity fixes (found while mirroring):** the old Rust loader was
more lenient than Go's in ways that would have defeated validation:

- negatives were silently clamped to 0 (so `-5` looked like `0`),
- a wrong-typed value (`"5"`, `true`) was silently ignored,
- a fractional value for an integer field was truncated.

Rust now reads into a signed intermediate struct (`Fields`), validates, then
converts to `Params`. So in both languages a wrong-typed value, a fraction for
an integer field, or a non-object section is an error, `null` leaves the
default, and unknown keys are ignored.

Files: `go_src/internal/params/params.go`, `rust_src/src/params.rs`.

## 2. Go geometric restart threshold saturates

Go's `int(float64)` is implementation-defined for values >= 2^63. I confirmed
on this machine (amd64) that the old formula wraps:
`int(100 * 1.5^400)` printed `-9223372036854775808`, which would make every
restart due immediately. New helper `geometricRestartThreshold` checks the
float first: NaN gives 0, `v >= 2^63` gives `math.MaxInt64`, otherwise
`int(v)`. Rust already saturated (at `usize::MAX`), so the saturation points
differ, but both are unreachable in practice (1.5^n needs n about 100+ restarts
and a matching ~1e19 conflicts).

I used the direct float check, not log-space, as you allowed: 2^63 is exactly
representable (`9223372036854775808.0`), so the comparison is exact.

Files: `go_src/internal/cdcl/cdcl.go`, `rust_src/src/cdcl/mod.rs` (test only).

## 3. CLI help and docs

- Both `--internal-params` help paragraphs now describe range checking and list
  every range (same list in Go and Rust; the surrounding prose already
  differed between the two languages and I left it alone).
- `docs/internal-parameters.md` (the `glucoseWindowSize` row): added "Must be at
  least 1 (and at most 10,000,000, since the window is allocated up front)."
  The other rows of that doc do not list ranges; you may want them added.

## 4. Tests (mirrored Go and Rust)

- Range table: 27 rejected cases (one per bound, plus wild values) and 14
  accepted boundary cases (`TestLoadRejectsOutOfRangeParameters` /
  `TestLoadAcceptsBoundaryParameters` and Rust `load_rejects_out_of_range_parameters` /
  `load_accepts_boundary_parameters`).
- Defaults valid; NaN/Inf rejected; first-violation-in-field-order;
  wrong JSON types rejected and `null` accepted.
- `TestGeometricRestartThresholdSaturates` and
  `test_restart_threshold_geometric_saturates` (ordinary terms, +Inf power,
  monotonic over 400 restarts; Go also covers exactly 2^62, 2^63, huge base, NaN).

## 5. Verification

- Go: `gofmt -l` clean, `go vet ./...` clean, `go test ./...` passes,
  `go test -race` on `params`, `cdcl`, `cliargs` passes. `util/paramtune` vet
  and tests pass (its sweep values are user-supplied, none hard-coded out of
  range, so it needed no change).
- Rust: `cargo fmt --check` clean, `cargo clippy --release -D warnings` clean,
  `cargo test --release`: 297 passed, 0 failed.
- End to end (final binaries): `varActivityDecay` 2.0 and 0, `lubyBaseConflicts`
  -5, `glucoseWindowSize` 1e11 all exit 1 with identical text in both languages;
  `null`, `{}` and `bveWorkBudgetFactor: 0` run normally; the file written by
  `--reset-internal-params` is byte-identical between Go and Rust and loads
  in both; a default run on `uf250-0100` returns SATISFIABLE in both.
- I did not rerun the 726-run benchmark checker or the differential fuzz from
  Stage 55: this stage changes only configuration loading and one saturating
  conversion that is unreachable on real runs, so solver behavior with the
  defaults is unchanged.

## 6. Known gaps

- **Error text for type errors differs between languages.** Go reports
  encoding/json's message (`json: cannot unmarshal string into Go struct field
  ...`); Rust reports `cdcl.glucoseK must be a number`. Both are errors; range
  errors are identical. Making them identical would need hand-written Go
  checking, which I judged out of scope.
- **Integral floats:** Rust accepts `1e3` for an integer field (it is an
  integer), Go's decoder rejects it. Edge case, not unified.
- **No overflow checking** of products such as `lubyBase * lubyTerm` or
  `factor * len(clauses)`; a huge in-range value (e.g. `bveWorkBudgetFactor` near
  2^62) could overflow. The ranges only have lower bounds for these, as you did
  not ask for upper bounds. Want upper caps?
- Only the first violation is reported per load.

## 7. REPORT55 answers acknowledged

N=8 stays (SMP testing later); the Rust 32-thread slowdown is attributed to
the laptop; you will look for structured UNSAT benchmarks. No action taken.

## 8. Things to know

- The Bash tool's login shell here is tcsh, so my build commands run under
  `bash -c`. Temporary binaries and probe configs lived in the session
  scratchpad; `git status` shows only the nine intended modified files plus
  your `low_priority.md`, `prompts/STAGE56.md` and `todo.md`.
