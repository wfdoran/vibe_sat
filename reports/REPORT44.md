# Report: Stage 44

## Summary

`STAGE44.md` asked for two small, concrete CLI changes, plus responded
to `REPORT43.md`'s open questions:

1. **`--alg-params`'s underscore syntax**: any positional value may now
   be given as `_` to mean "use this slot's own default," without
   needing to know or spell out what that default actually is. This
   directly fixes the "positional wrinkle" `REPORT43.md` itself flagged
   — reaching `cdcl`'s `val4` (phase strategy) used to require also
   specifying a real, if unwanted, `val3` memory limit; now
   `--alg-params _ _ _ 3` reaches it while leaving `val1`/`val2`/`val3`
   all at their defaults.
2. **Restart-strategy values 4 and 5 swapped**, and round-robin now
   spans all four fixed/data-driven schedules instead of three:
   `val2=4` is now Glucose's data-driven policy (previously 5),
   `val2=5` is now round-robin (previously 4), and round-robin's
   rotation grows from `{polynomial, geometric, Luby}` to `{polynomial,
   geometric, Luby, Glucose}`. This directly answers `REPORT43.md`'s
   third open question (about phase round-robin's diversity value):
   restart round-robin's new period (4) is now deliberately coprime
   with phase round-robin's existing period (3), so every worker up to
   the twelfth (`lcm(4,3)`) gets a unique (restart, phase) pairing
   before either rotation repeats — a structural diversity broadening
   that needed no dedicated benchmark to justify.

Both languages, fully verified, no default *behavior* changed for
anyone not using the swapped numerals or underscore explicitly.

## Change 1: underscore for "use the default"

`Args.AlgParams`'s element type changed from a plain integer to "an
integer, or explicitly unset" (Go: `[]int64` → `[]*int64`, a `nil`
element meaning `_`; Rust: `Vec<i64>` → `Vec<Option<i64>>` inside the
existing `Option`-wrapped field). This is a different thing from a
position simply never being reached at all (a shorter slice) — but
every consumer treats the two identically: "fall through to whatever
this slot's own default-resolution logic already does." Concretely:

- A new `parseAlgParamValue`/equivalent helper: `_` parses to `None`/
  `nil` rather than an error; anything else goes through the existing
  integer (or, for `cdcl`'s `val3`, byte-size) parsing path unchanged.
- Every range-validation check (`validate()` in Go, the equivalent in
  Rust) skips entirely for an unset element — there's nothing to
  range-check about "use the default."
- Every consumption site (`main.go`'s `runHillClimb`/`runWalkSat`/
  `runDFS`/`runCDCL`, and Rust's equivalents — 9 call sites in Go)
  checks for "unset" before reading a value, leaving whatever default
  was already in place untouched.
- `--alg-params _` alone still counts as "the flag was given" for
  `hc`/`ws`'s "at least one of `--alg-params` or `--time-limit-secs`"
  requirement — the slice is non-empty (length 1), even though its one
  element is unset.

This is a purely-additive parsing change: any command line that didn't
use `_` before behaves identically, since every existing consumer's
logic for "value present and in range" vs. "value absent" is
unchanged — only "value present but explicitly deferred to the
default" is new.

## Change 2: restart-strategy renumbering, and round-robin absorbs Glucose

| | Before | After |
|---|---|---|
| `val2=4` | round-robin (3-way: polynomial/geometric/Luby) | Glucose (data-driven, unchanged behavior) |
| `val2=5` | Glucose (data-driven, unchanged behavior) | round-robin (4-way: polynomial/geometric/Luby/Glucose) |

The rationale `STAGE44.md` gave for the swap itself: round-robin is
the one "meta" choice in this list — it isn't a schedule on its own,
it's a way of combining the others — so it makes sense for it to keep
the *highest* numeral as the list of real strategies grows, rather
than sitting in the middle of it the way `4` did before Glucose folded
into its own rotation.

The more consequential part is folding Glucose into the rotation
itself. `roundRobinStrategies` grew from 3 entries to 4
(`resolveRestartStrategy`'s modulo: `%3` → `%4`), so a multi-threaded
run's default now spreads across four restart schedules per worker,
not three. Combined with `PhaseRoundRobin`'s own existing 3-way
rotation (`STAGE43.md`, unchanged this stage), this is what actually
answers `REPORT43.md`'s third open question:

> The multi-threaded `PhaseRoundRobin` default carries over restart's
> diversification precedent by analogy, not independent multi-threaded
> measurement — worth a dedicated benchmark of multi-threaded `cdcl`
> with vs. without phase diversification before trusting it?

Rather than running that benchmark, `STAGE44.md` chose a structural
fix: since both rotations resolve from the *same* worker thread index,
having periods 4 and 3 — `gcd(4, 3) = 1` — means the *combined*
(restart strategy, phase strategy) pair assigned to worker `i` is
unique for `i = 0` through `11` (`lcm(4, 3)`) before any repeat, versus
the previous 3-and-3 rotation, where both patterns would have repeated
in lockstep every 3 workers regardless of how many threads a run
actually used. This doesn't independently prove phase round-robin's
diversity value the way a dedicated benchmark would, but it does
maximize how much genuine diversity a reasonably-sized thread pool
gets *given* that both rotations exist — a cheap, purely combinatorial
improvement that required no new measurement to justify.

## Verification

- **Go**: `go build ./...`, `go vet ./...`, `gofmt -l .` clean;
  `go test ./...` (all ten packages) and `go test -race -count=2
  ./internal/cdcl/...` clean. Four new tests in
  `internal/cliargs/args_test.go` covering underscore in a middle
  slot, underscore reaching `cdcl`'s `val4`, underscore for the memory
  limit specifically, and underscore alone satisfying `hc`'s "at least
  one of" requirement; `TestResolveRestartStrategyRoundRobin` extended
  to the new 4-entry rotation; `TestParseCDCLAcceptsGlucoseRestartStrategy`/
  `TestParseCDCLAcceptsRoundRobinRestartStrategy` renumbered to match
  the swap.
- **Rust**: `cargo build --release`, `cargo build --all-targets`,
  `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`
  clean; `cargo test --release` — 250 passed, 0 failed (was 246 before
  this stage).
- **A second, independent bug caught during the Rust port**:
  `cdcl/mod.rs`'s verbose-announcement formatting
  (`describe_params`/Go's equivalent) turned out to have its own
  separate, hardcoded enum-to-numeral mapping for the `"cdcl: ..."`
  line, distinct from the parsing-side mapping this stage's change
  actually touches. The parsing swap alone left this second mapping
  stale, so Rust's announcement line briefly printed the *old*
  numerals (e.g. `restart=4` for what parsing correctly resolved as
  round-robin) even though the actual restart behavior was already
  correct. Caught via the manual CLI smoke test below, fixed directly.
- **Manual CLI smoke test, both languages**: `--alg-params _ _ _ 3`
  (underscore reaching `val4`) shows `phase=3` with `select_var`/
  `restart` at their own defaults; `--alg-params _ 5` shows `restart=5`;
  `--alg-params 2 4` (new Glucose numeral) solves correctly;
  `--alg-params _` alone satisfies `hc`'s requirement; `--alg-params 2
  6` is still rejected (0-5 remains the valid range, only the meaning
  of 4/5 changed); `--alg-params __`/`_x` are still parse errors (only
  the exact token `_` is special); `--num-threads=6` with no explicit
  `--alg-params` shows `restart=5 phase=3` (both round-robin defaults).
- **Cross-language check, done directly**: both binaries, `--alg-params
  2 4` and `--alg-params _ _ _ 3`, against the same instance —
  byte-identical verdicts and verbose announcement lines.

## Documentation

- `docs/usage.md` — `--alg-params`'s intro paragraph documents `_`;
  the `cdcl` table's `val2` row and the paragraph below it updated for
  the swap; a new paragraph explains the coprime-rotation-periods
  reasoning.
- `docs/background.md` — "Restart strategies" section restructured so
  Glucose (now `val2=4`) is introduced before round-robin (now
  `val2=5`, now described as cycling through all four); "Phase-
  selection strategies" section's round-robin paragraph extended with
  the coprime-periods reasoning.
- `docs/references.md` — the two `--alg-params val2=5` mentions
  (Glucose's own citation) corrected to `val2=4`.
- `docs/internal-parameters.md` — no change needed; no parameter
  values or defaults changed, only which CLI numeral selects which
  already-existing strategy.

## Housekeeping note

While preparing this stage, I noticed `go_src/internal/cdcl/phase_test.go`
(`REPORT43.md`'s new test file, 10 tests) was not included in the Stage
43 commit (`a3c1483`) — it shows as untracked rather than committed. It
was still present in the working tree from that stage, so this stage's
own edits (`TestResolveRestartStrategyRoundRobin`, in
`parallel_test.go`, not `phase_test.go`) didn't depend on it being
committed, but you'll want to include it when you commit this stage's
changes, since it's real, currently-passing test coverage that
otherwise isn't in your history yet.

## Questions for you

- The restart/phase round-robin coprime-periods trick works well for
  exactly two rotating dimensions. If a third portfolio dimension
  (e.g. `SelectVar`, which already has exactly 4 possible values) ever
  gets its own round-robin option, worth planning for three mutually
  coprime periods from the start, or cross that bridge if/when it
  actually comes up?
- You mentioned switching from round-robin to random assignment "at
  some point" — is that still just a note for later, or is there a
  specific reason (e.g. round-robin's determinism making some worker
  index systematically luckier or unluckier across many runs) you'd
  want investigated first?
