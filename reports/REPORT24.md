# Report: Stage 24

## Summary

Stage 24 improves the testing/benchmarking harness for both languages,
per `STAGE24.md`'s citation of `REPORT23.md`'s closing note (its new
`bench_test.go` covered only `cdcl`, and only in Go) and
`REPORT22.md`'s item 18 (a standing Go-vs-Rust benchmark harness,
replacing the one-off scripts every stage from `REPORT8.md` through
`REPORT23.md` wrote, used once, and discarded). No `vibe_sat` solving
behavior changed in either language; every change here is
infrastructure. Three pieces:

1. **A standing cross-language comparison harness**, `util/benchcompare/`
   -- a permanent tool (not a throwaway script) that builds and runs
   both binaries across a chosen slice of `benchmark/`, checks that
   they agree on every verdict, independently re-verifies every
   reported SAT solution from scratch, and reports timing.
2. **Go's profiling-benchmark coverage extended** from `cdcl`-only
   (`REPORT23.md`) to `dfs`, `hillclimb`/`ws`, and `preprocess`.
3. **The same profiling-benchmark infrastructure added to Rust**,
   which had none at all before this stage -- this required adding a
   `src/lib.rs` library target (see "A structural prerequisite" below).

## Where the harness lives

`STAGE24.md`'s ask runs into a real constraint: `PROMPT.md`'s project
layout says only `go_src`, `rust_src`, and `benchmark` may be written
to (`util/` was a one-off exception you granted in Stage 18, explicitly
scoped to throwaway spikes -- "a play ground for trying things out and
learning"). A *permanent*, cross-language driver tool doesn't cleanly
fit either "solver source code" or "throwaway spike," so rather than
guess, I asked directly; you chose extending `util/`, alongside
`util/termination/` and `util/clausesharing/`.

## `util/benchcompare/`

A single Go module (its own `go.mod`, matching `util/termination/go`'s
and `util/clausesharing/go`'s convention), **written once, in Go, not
mirrored in Rust.** This is a deliberate difference from
`util/termination/` and `util/clausesharing/`, which exist in both
languages: those validated an algorithm/data-structure choice that
mattered separately to each language's own future implementation
(work-stealing termination detection, a lock-free clause-sharing ring
buffer). `benchcompare` is a driver that shells out to two
already-built binaries -- there is no per-language design question a
second implementation would answer, only maintenance cost for no
benefit.

### What it does

```sh
cd util/benchcompare
go run . --dirs=uf250-1065,uuf250-1065 --algorithm=cdcl \
  --sample=20 --time-limit-secs=30
```

- Builds both binaries (`go build`/`cargo build --release`) unless
  `--go-bin`/`--rust-bin` point at pre-built ones.
- Recursively gathers `.cnf` files from the named `benchmark/`
  subdirectories (see "A real bug this caught" below for why
  recursive, not just direct-children), optionally sampling a fixed,
  seeded subset per directory for a manageable, reproducible sweep.
- Runs each binary **sequentially** (never both at once -- concurrent
  runs on the same machine would make wall-clock timing meaningless,
  especially once `--num-threads` is involved), always passing
  `--verbose=1` and a fresh `--output=<tmpfile>`, regardless of what a
  human would type by hand, so every run yields both a parseable
  verdict line and (when SAT) a solution file to check.
- Independently parses the CNF file and the solution file **from
  scratch** (`cnf.go`/`verify.go`) -- deliberately not importing
  `go_src`'s own `internal/cnf` (which, being `internal` in a
  different Go module, this tool structurally cannot import even by
  accident) -- and checks every clause is actually satisfied. This is
  the same principle every prior stage's throwaway verification script
  followed (`REPORT5.md` onward: "an independent solution verifier...
  never reusing solver code"), finally made permanent.
- Enforces a hard per-run timeout (`--hard-timeout-secs`, default
  `--time-limit-secs + 30s` or 120s) independent of `vibe_sat`'s own
  `--time-limit-secs`, so a bug or an unexpectedly hard instance can
  never hang the harness itself.
- Prints a summary table (solved counts, mean/median time, mismatches)
  and flags any file that needs a human look (a cross-language
  mismatch, a failed independent verification, or a crash/timeout
  kill); `--json` writes the full per-file detail (including complete
  stdout/stderr) for anyone who wants to slice the data differently.

### A real bug this caught, during its own dogfooding

Running a first real sweep across `uf100-430` + `uuf100-430`
(`--sample=15` each) reported **15 total files, all SAT** -- silently
missing every one of `uuf100-430`'s files rather than erroring. The
cause: `benchmark/`'s subdirectories are not all laid out the same
way. Most (e.g. `uf250-1065`) hold their `.cnf` files directly, but
four (`uuf100-430`, `uf75-325`, `uuf50-218`, `uuf75-325`) have an
extra nested folder level -- a leftover of how their original SATLIB
tarball extracted:

```
$ find benchmark/uuf100-430 -maxdepth 1
benchmark/uuf100-430
benchmark/uuf100-430/UUF100.430.1000
```

`selectFiles`'s first version only looked directly inside
`benchmark/<dir>`, so it silently found zero files in these four
directories instead of failing loudly. Fixed by switching to a
recursive `filepath.WalkDir`, which finds `.cnf` files at any depth
under the named directory without needing to special-case which
directories are flat and which aren't; re-running the same sweep
afterward correctly found all 30 files (15 SAT + 15 UNSAT). Added
`TestSelectFilesFindsNestedFiles` as a regression test, and left
`benchmark/`'s own layout untouched, per `PROMPT.md`'s rule that I may
not alter benchmark files -- this was a harness-side bug, not a
benchmark-data problem.

### Verification

- Unit tests (25 subtests) covering the independent CNF parser (valid
  formulas, the SATLIB `%` end-of-clauses marker, every error case),
  the independent solution parser and clause checker, `runConfig`'s
  argument construction, verdict-line parsing (including that
  `"SATISFIABLE"` on a solution-file status line is never mistaken for
  the bare `"SAT"` verdict line), summary aggregation, report
  rendering, project-root auto-detection, and file
  selection/sampling (including the nested-directory case above). `go
  build`, `go vet`, `gofmt -l`, and `go test ./...` are all clean.
- Real end-to-end sweeps against the actual built binaries: `dfs`
  across 5 sampled `uf20-91`/`uuf50-218` files, `cdcl` at
  `--num-threads=4` across 3 sampled `uuf250-1065` files, and `cdcl`
  across 30 files spanning `uf100-430`/`uuf100-430` -- **0
  cross-language mismatches, 0 independent-verification failures** in
  every sweep. Also confirmed the hard-timeout kill path fires and is
  reported distinctly from a real crash (`--hard-timeout-secs=2`
  against a `cdcl` run given a much longer `--time-limit-secs`).

## Go: profiling-benchmark coverage extended to dfs/hillclimb/preprocess

New `bench_test.go` files in `go_src/internal/dfs`,
`go_src/internal/hillclimb`, and `go_src/internal/preprocess`, in the
same style `REPORT23.md` established for `cdcl` (ordinary Go
benchmarks, skipped by a plain `go test`, run explicitly via
`-bench`):

- **`dfs`**: `benchmark/uuf175-753/uuf175-083.cnf` (`REPORT19.md`'s
  hardest file in that directory that still actually *finishes*
  proving UNSAT in a reasonable time -- `uuf250-1065` instances never
  finished single-threaded even at 120s, per `REPORT18.md`, which would
  make benchmarking unpredictably slow). Single-threaded and 8-worker
  variants.
- **`hillclimb`/`ws`**: a representative `uf100-430` file, with a fixed
  200-restart/try budget rather than relying on the instance's own
  difficulty -- `hc`/`ws` are incomplete searches with no natural
  "done" point to time against, unlike `dfs`/`cdcl`. Single-threaded
  and 8-worker variants of both `hc` and `ws`.
- **`preprocess`**: `benchmark/blocksworld/bw_large.c.cnf` -- the exact
  instance `REPORT21.md`'s "Open questions" flagged as spending ~11s in
  preprocessing alone (`--no-preprocessing`: 11s -> 34ms), and
  `REPORT22.md`'s item 2 named as real evidence that preprocessing, not
  any solving algorithm, is the bottleneck on some real instances. This
  benchmark exists so a future stage pursuing that item has a profile
  to start from rather than re-deriving one from scratch.

All four ran cleanly with `-bench=. -benchtime=1x` and produced
sensible numbers (e.g. `preprocess`'s `bw_large.c.cnf` benchmark: ~12.9s
on this machine, consistent with `REPORT21.md`'s finding).

## Rust: the same infrastructure, from nothing

Rust had no benchmark infrastructure of any kind before this stage --
`REPORT23.md`'s own `cdcl` profiling used temporary, add/measure/revert
`Instant` instrumentation specifically because nothing permanent
existed yet. This stage adds `rust_src/benches/{dfs,cdcl,hillclimb,preprocess}_bench.rs`,
mirroring the four Go benchmarks above file-for-file (same instances,
same parameters, same variants).

### A structural prerequisite: `src/lib.rs`

`rust_src` had no library target -- every module was declared with
plain `mod x;` directly inside the `vibe_sat` **binary** crate
(`src/main.rs`). Rust's `benches/` directory compiles each file as its
own separate binary, which can only reach another crate's code by
depending on it as a **library** -- there is no Rust equivalent of Go's
`internal/` packages being directly importable by a `_test.go`/
`bench_test.go` file that already lives inside the same package. So
this stage adds `src/lib.rs` (`pub mod assignment; pub mod cdcl; ...`
-- every module, re-exported) and changes `main.rs`'s module
declarations from `mod cdcl; ...` to
`use vibe_sat::{assignment, cdcl, ...};`. This is a pure
reorganization: no function, type, or piece of solving logic moved or
changed, only where the module tree is rooted. Verified this changed
nothing observable: `cargo build --release`, `cargo clippy --all-targets
-- -D warnings`, and `cargo fmt --check` are all clean; `cargo test
--release` still passes all 190 tests (now running under the library
target instead of the binary target); and the compiled binary's output
on a real file is unchanged (checked byte-for-byte against a run from
before this change).

### Why not criterion

Rust's stable toolchain has no built-in `#[bench]` the way Go always
has `testing.B`, and the standard answer to that gap is the `criterion`
crate (a general-purpose benchmarking tool, not SAT-specific, so
allowed under `PROMPT.md`'s crate rule). I evaluated it and decided
against it: criterion enforces a **minimum of 10 timed samples** per
benchmark, and this project's own profiling instances are multi-second
to begin with (the `cdcl` instance alone is ~20-27s single-threaded,
per `REPORT21.md`/`REPORT23.md`/this stage's own measurement) -- 10
samples of that would mean 200+ seconds just to benchmark once, before
even reaching for a profiler. `REPORT23.md`'s own methodology already
found that this project's actual need is Go's `-benchtime=1x`
single-shot timing, specifically *because* repeated-sampling timing was
either too slow or too noisy to trust for a run this long. So instead,
each Rust bench file is a plain binary (`harness = false`, no
statistics library at all) that runs each named benchmark once (or a
small fixed count, matching the Go side), timed manually with
`std::time::Instant`, and prints a result line in the same format Go's
own benchmark output uses. This is the permanent version of exactly
what `REPORT23.md` did temporarily, not a mismatched tool bolted on
because it's the "standard" choice.

Each bench binary's doc comment documents how to profile it with
`perf record` (now usable, per `STAGE24.md`'s note that
`kernel.perf_event_paranoid=1` is set on the target machine) -- I did
not run `perf` myself this stage, since `STAGE24.md` asks to *improve
the harness*, not to re-profile; that capability is now in place for
whichever future stage does.

### Verification

All four Rust benchmarks were run once each and produced sensible,
qualitatively consistent results with prior reports (e.g. `cdcl`:
27.3s single-threaded, 11.3s at 8 threads, 15.6s at 32 threads --
the same oversubscription regression shape `REPORT21.md` found).
`cargo build --release --benches`, `cargo fmt --check`, `cargo clippy
--all-targets -- -D warnings`, and `cargo test --release` (190 tests)
are all clean.

**One number is not directly comparable and I'm flagging it rather
than glossing over it**: the Rust `preprocess` benchmark measured
~22.8s on `bw_large.c.cnf` against Go's ~12.9s on the same file --
Rust *slower* than Go, which cuts against every prior head-to-head
result in this project (`REPORT10.md` onward has consistently found
Rust faster on `dfs`/`cdcl`). I did not investigate this further --
doing so properly would mean profiling preprocessing specifically,
which is `REPORT22.md`'s still-open item 2, not this stage's own scope
-- but I don't want to present it as a confirmed finding either. It
could be a genuine algorithmic difference in this project's
preprocessing code between the two languages, sandboxed-VM noise (this
session's builds ran under redirected `GOCACHE`/`CARGO_HOME` due to a
read-only home directory in this sandbox, an environment artifact
unrelated to the two languages themselves), or something else. Worth
a real look if/when `REPORT22.md`'s preprocessing item is picked up.

## Command line arguments

None. This stage changes no `vibe_sat` behavior or CLI surface in
either language.

## Testing

- Go (`go_src`): `go build ./...`, `go vet ./...`, `gofmt -l .` clean;
  `go test ./...` (9 packages) clean; new `-bench` targets in `dfs`,
  `hillclimb`, and `preprocess` run cleanly with `-benchtime=1x`.
- Rust (`rust_src`): `cargo build --release`, `cargo build --release
  --benches`, `cargo fmt --check`, `cargo clippy --all-targets -- -D
  warnings` clean; `cargo test --release` (190 tests) clean; all four
  new `cargo bench --bench <name>` binaries run cleanly.
- `util/benchcompare`: `go build ./...`, `go vet ./...`, `gofmt -l .`
  clean; `go test ./...` (25 subtests) clean; real sweeps against both
  built binaries as described above.

## Open questions / notes for you

- **The Rust `preprocess` benchmark's slower-than-Go number** (above)
  is worth a real look once `REPORT22.md`'s item 2 (parallelize
  preprocessing) is picked up -- I did not investigate the cause this
  stage, since it wasn't this stage's scope and I didn't want to guess.
- I found (but did not touch, since `PROMPT.md` says I may not alter
  anything under `prompts/`) a stray editor backup file,
  `prompts/STAGE24.md~`, sitting alongside `STAGE24.md` -- you may want
  to remove it yourself.
- `util/benchcompare` is written once, in Go, not mirrored in Rust; see
  "Where the harness lives" above for the reasoning. Let me know if
  you'd rather have a Rust version too (e.g. for symmetry, or if you'd
  simply rather drive comparisons from a Rust tool) -- I made a
  judgment call here rather than asking a second time this stage.
- Per your note that `perf_event_paranoid=1` is now set: nothing in
  this stage actually invokes `perf` itself (this stage is about the
  harness, not a new profiling pass) -- but every new Rust bench binary
  documents how to point `perf record` at it, so that capability is
  ready whenever a future stage wants it.
- No language/toolchain version changes needed. `criterion` was
  evaluated and deliberately *not* added as a dependency; see "Why not
  criterion" above.
