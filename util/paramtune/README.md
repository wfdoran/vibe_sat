# paramtune

`vibe_sat`'s internal tuning constants (restart-schedule bases, decay
rates, the Glucose restart policy's K, preprocessing's work-budget
factors -- see `docs/internal-parameters.md`) became runtime-
configurable via `.vibe_sat.json` in `STAGE39.md`. `paramtune` is the
"auto-tuning harness" that stage also asked for (item 9, sequenced
after `REPORT22.md`'s item 7 parameter inventory): it sweeps candidate
values for one or more of those parameters against a chosen slice of
`benchmark/`, using `util/benchcompare` as the actual measurement
engine, and reports which candidate value(s) perform best.

It lives here, under `util/`, alongside `benchcompare`, per
`prompts/PROMPT.md`'s Stage 18 carve-out for permanent side tooling
that `go_src`/`rust_src` must not depend on.

## Quick start

From anywhere inside the repository:

```sh
cd util/paramtune
go run . --dirs=uf20-91 --algorithm=cdcl --time-limit-secs=10 \
  --sweep="cdcl.glucoseK=0.4|0.6|0.9"
```

This builds the Go binary, the Rust binary (release mode), and
`benchcompare` itself once (unless `--go-bin`/`--rust-bin`/
`--benchcompare-bin` point at pre-built ones), then runs a
`benchcompare` sweep across `benchmark/uf20-91` once per candidate
value of `cdcl.glucoseK`, each time with a fresh `.vibe_sat.json` set
to that value and everything else at its default. It prints a table
of every candidate tried and which one won.

Use `--list-params` to see every parameter path `--sweep` accepts:

```sh
go run . --list-params
```

## How a candidate's config actually reaches `vibe_sat`

No `--internal-params` flag is involved. `benchcompare`'s own `run()`
execs the `vibe_sat` binaries with no working directory of its own
set, so they inherit whatever directory `benchcompare`'s process was
started in. `paramtune` writes each candidate's `.vibe_sat.json` into
a fresh scratch directory and starts `benchcompare` *from* that
directory (`exec.Cmd.Dir`) -- which is exactly `vibe_sat`'s own
implicit-config discovery path (`STAGE39.md`'s `params.Resolve`:
"a `.vibe_sat.json` left lying around is picked up with no flag
needed"). This needed no changes to `benchcompare` itself.

One consequence worth knowing: if one language's `vibe_sat` binary
predates the `.vibe_sat.json` support (or is somehow built without
it), that language's numbers will simply be flat across every
candidate in a sweep, since it never reads the file at all -- not a
bug in `paramtune`, just a sign the binary in use doesn't implement
the config system yet.

## `--sweep`'s mini-language

Each parameter path is written exactly as its `.vibe_sat.json` key
would be, dotted with its section: `cdcl.glucoseK`,
`preprocess.bveWorkBudgetFactor`, etc.

- `path=v1|v2|v3` -- sweep one parameter across a `|`-separated list
  of candidate values.
- `path1=v1|v2,path2=w1|w2` -- a **joint grid**: every parameter in a
  comma-separated group is swept together, as the full cartesian
  product of their candidate lists (2x2 = 4 combinations here).
- `stage1;stage2` -- semicolon-separated **stages**, run in order.
  Each stage's winning combination is fixed (carried forward as an
  override) before the next stage's candidates are built and run.

Example -- two sequential single-parameter stages (coordinate
descent):

```sh
--sweep="cdcl.glucoseK=0.4|0.6|0.9;preprocess.bveWorkBudgetFactor=1000|2000|4000"
```

Example -- one joint-grid stage:

```sh
--sweep="cdcl.glucoseK=0.5|0.6,cdcl.lrbAlpha=0.3|0.4"
```

### Why coordinate descent is the default, not one big joint sweep

Per `docs/internal-parameters.md`'s design notes (`STAGE39.md`'s own
open question): sweeping every combination of even a handful of
continuous-valued parameters is combinatorially expensive against
real benchmark instances, and this project's existing tuning work
(`REPORT35.md`'s `glucoseK` sweep, done manually before this tool
existed) has never found evidence these parameters interact strongly
enough to need a joint search. `paramtune` therefore defaults to one
parameter (or one small, explicitly-requested group) at a time,
carrying the winner forward -- the joint-grid syntax above still
exists for the rare case two parameters are suspected to interact,
but it's opt-in, not automatic.

## Ranking candidates

A candidate's score, computed from `benchcompare`'s own `--json`
report, ranks in this order (lower is better at every level; an
earlier criterion always outranks a later one):

1. **Unsolved** -- files where either language failed to reach a
   definite SAT/UNSAT verdict. A tuning sweep must never be allowed to
   "win" by making the solver less complete, only faster at the same
   job.
2. **Issues** -- cross-language verdict mismatches or failed
   independent solution verifications (`benchcompare`'s own
   correctness checks). Also a hard red flag, ranked ahead of speed.
3. **Total elapsed time** -- summed `ElapsedSeconds` across every
   solved run, both languages. This only ever breaks a tie between
   two candidates that are otherwise equally correct and complete.

The full table (not just the bare winner) is always printed, so a
close call or a surprising result is visible, not hidden behind a
single number.

## Flags

| Flag | Meaning |
|---|---|
| `--sweep` | The sweep to run (required) -- see the mini-language above |
| `--list-params` | Print every known parameter path and exit |
| `--write-result-dir` | After sweeping, write the winning configuration as `.vibe_sat.json` into this directory |
| `--project-root` | Repository root (default: auto-detected) |
| `--go-bin` / `--rust-bin` / `--benchcompare-bin` | Pre-built binaries to use instead of building fresh ones (recommended for a sweep with many candidates -- building happens once regardless, but supplying your own skips it entirely) |
| `--dirs` / `--paths` / `--sample` / `--seed` / `--max-size-mb` | Passed through to `benchcompare` verbatim -- see its own README.md |
| `--algorithm` | `vibe_sat --algorithm` value (required) |
| `--alg-params` / `--time-limit-secs` / `--num-threads` / `--no-preprocessing` / `--hard-timeout-secs` | Passed through to `benchcompare` verbatim |

## Design notes

- **A small, independent copy of the `.vibe_sat.json` shape**
  (`params.go`) is duplicated here rather than imported from
  `go_src/internal/params` -- like `benchcompare`'s own `cnf.go`/
  `verify.go`, this tool is a separate Go module and structurally
  cannot import an `internal` package from a different module anyway.
  Keep it in sync by hand with `go_src/internal/params/params.go` and
  `docs/internal-parameters.md` if either changes.
- **The Go, Rust, and `benchcompare` binaries are built once**, up
  front, regardless of how many candidates a sweep has -- not once
  per candidate. A sweep with many candidates would otherwise pay a
  full rebuild (Rust's release build in particular) for every single
  one.
- **Every run is still sequential** (one candidate's `benchcompare`
  sweep completes fully before the next starts), for the same reason
  `benchcompare` itself runs Go and Rust sequentially: concurrent runs
  on the same machine would make wall-clock comparisons meaningless.
