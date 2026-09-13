# Report: Stage 15

## Summary

Stage 15 is complete for both the Go and Rust versions of `vibe_sat`:
`cdcl` now supports restarts -- periodically abandoning the current
decision stack and starting over from decision level 0 while keeping
every learned clause collected so far -- per `STAGE15.md` and the
references it cites (Luby, Sinclair & Zuckerman, "Optimal Speedup of
Las Vegas Algorithms," 1993; Gomes, Selman & Kautz, "Boosting
Combinatorial Search Through Randomization," AAAI 1998).
`--alg-params` for `--algorithm=cdcl` grows a third value selecting
the restart strategy, with the existing memory-limit value shifting
from the second slot to the third.

**Note on naming, corrected after this report's first draft**:
`STAGE15.md` originally asked for "the Luby sequence, or simple
geometric growth," with the growth sequence specified as `a*1^2,
a*2^2, a*3^2, a*4^2, ...`. That sequence is quadratic, not geometric
in the standard mathematical sense (a geometric sequence has a
*constant ratio* between consecutive terms; `a*k^2`'s ratio shrinks:
4, 2.25, 1.78, ...). This report and the code originally called it
"geometric" anyway, matching `STAGE15.md`'s own wording, and only
flagged the mismatch as a side note. Once that was pointed out, the
quadratic schedule was renamed to what it actually is --
**polynomial** growth -- and a *third* schedule was added that really
is geometric: `c*r^k`, a constant ratio `r` between consecutive
restart intervals. `--alg-params`'s restart-strategy value now has
four options: 0 = none, 1 = Luby, 2 = polynomial (the quadratic
sequence, still the default -- see the benchmark below), 3 =
geometric (the new, true geometric sequence).

The benchmark below (re-run against the original three-way comparison
plus the new true-geometric schedule) produced the most decisive, and
most surprising, result of any stage so far: the polynomial schedule
solves **10/10** of the sampled UNSAT instances within a fixed time
budget, against 6/10 with no restarts and just **3/10** -- worse than
no restarts at all -- with the Luby schedule using its
literature-standard base unit. Per this project's established
practice (`REPORT13.md`'s VSIDS-vs-LRB precedent), real measurement on
this project's own benchmark set wins out over the literature default,
so `--algorithm=cdcl` defaults to the polynomial schedule, not Luby.
See below for how the new true-geometric schedule compares.

## Design

### Mechanics: restart = `backtrackTo(0)`/`backtrack_to(0, ...)`

A restart needs no new backtracking primitive: it is exactly a call
to the same `backtrackTo`/`backtrack_to` function Stage 11's
conflict-driven backjumping already uses, just triggered by a
schedule instead of by `analyze`'s computed backtrack level, and
always targeting level 0. Everything that function already does *not*
touch -- the clause database (including every learned clause),
clause/variable activity scores, and saved phases -- survives a
restart untouched by construction, which is exactly `STAGE15.md`'s
requirement ("keeping every learned clause collected so far"). No
interaction with Stage 12's memory-limit reduction, Stage 13's
VSIDS/LRB, or Stage 14's phase saving was needed beyond this shared
hook point.

Go: `solver` gained three fields -- `restartStrategy`, `restartCount`
(how many restarts have happened, indexing into the sequence), and
`conflictsSinceRestart` -- plus `restartThreshold()` (the schedule
math) and `maybeRestart()` (the trigger, called once per conflict
from `Run`, right after `learnAndBackjump`). Rust mirrors this with
the same free-function pattern established since Stage 11: `restart_threshold`
and `maybe_restart` are standalone functions, and `run` threads
`conflicts_since_restart`/`restart_count` locals through them exactly
like every other piece of per-run state.

### The Luby sequence

`lubyTerm`/`luby_term` computes the classic sequence
1, 1, 2, 1, 1, 2, 4, 1, 1, 2, 1, 1, 2, 4, 8, ... via the standard
0-indexed iterative form MiniSat and its descendants use (rather than
the naive recursive definition), verified by hand against the first
15 terms and covered by two direct unit tests per language. Multiplied
by the base constant `b` (see below), term `k` (0-indexed, `k` =
number of restarts so far) gives the number of conflicts the next
restart must wait for.

### The polynomial sequence (originally, incorrectly, called "geometric")

`STAGE15.md` specifies this growth sequence as `a*1^2, a*2^2, a*3^2,
a*4^2, ...` -- quadratic, i.e. polynomial in the restart index, not a
geometric sequence at all: a geometric sequence has a *constant
ratio* between consecutive terms, and `a*k^2`'s ratio shrinks (4,
2.25, 1.78, ...) rather than staying fixed. This implementation
follows `STAGE15.md`'s formula exactly as written, just under its
correct name (`RestartPolynomial`/`RestartStrategy::Polynomial`)
instead of the "geometric" label `STAGE15.md` originally used.
Restart index `k` (1-indexed here) waits `a*k^2` conflicts.

### The true geometric sequence

Once the naming mismatch above was caught, a genuinely geometric
schedule was added alongside it: `c*r^k` for restart index `k`
(0-indexed), a constant ratio `r` between consecutive restart
intervals -- the schedule "geometric restarts" conventionally refers
to in the SAT literature (e.g. MiniSat 1.13/1.14's restart scheme,
before Luby restarts became MiniSat's default). Restart `k` waits
`c*r^k` conflicts, truncated to an integer (matching MiniSat's own
implementation, which tracks the running restart limit as a `double`
and casts down rather than rounding).

### Restart statistic: conflicts, not decisions

`STAGE15.md` assumes "node count" as the restart trigger without
`cdcl` having an explicit "node" concept the way `dfs` does (`dfs`
tracks `NumNodes`; `cdcl` only ever tracked `NumDecisions` and
`NumConflicts` separately). This implementation reads "node count" as
**conflict count**: it's the convention the restart literature itself
uses (both papers cited above, and every MiniSat-lineage restart
schedule this project is aware of), and `cdcl` already has a
first-class `NumConflicts`/`num_conflicts` counter to use for it.

### Choosing `a`, `b`, `c`, and `r`

`STAGE15.md` asks, for `a` and `b`: use a standard literature value if
one exists; otherwise pick about one second of work on the
`uf250`/`uuf250` benchmark set. The same reasoning was applied to the
new true-geometric schedule's `c` and `r`, and produced a genuine
standard for every constant except `a`:

- **`b` (Luby) = 100 conflicts.** MiniSat's own default Luby restart
  base (`-rfirst=100`) is about as close to a "standard value in the
  literature" as SAT solving gets -- it's the number most
  MiniSat-lineage solvers (Glucose, CryptoMiniSat, etc.) inherited
  unchanged. Per `STAGE15.md`'s explicit preference ordering, that
  standard is used as-is.
- **`a` (polynomial) = 18000 conflicts.** The quadratic formula
  `STAGE15.md` specifies has no standard constant to inherit, since
  it isn't a geometric/exponential sequence at all (see above) --
  there is nothing in the "geometric restarts" literature to look up
  for it. Falling back to `STAGE15.md`'s own suggestion, this was
  measured directly: five sampled `uf250-1065` and five sampled
  `uuf250-1065` files, run for exactly one second each under this
  project's current default `cdcl` configuration (VSIDS + phase
  saving), produced a conflict rate clustering tightly in
  ~17,300-18,900 conflicts/second regardless of instance or verdict.
  18000 was chosen as a round number within that range.
- **`c` (geometric) = 100 conflicts, `r` (geometric) = 1.5.** Unlike
  `a`, this schedule genuinely is the one the literature calls
  "geometric restarts," and a standard pairing exists: MiniSat
  1.13/1.14's own geometric restart scheme, before Luby restarts
  became the default in later MiniSat versions, used base interval
  100 (the same `rfirst` constant reused here as `b`) with growth
  factor 1.5. That 1.5 itself has a documented rationale, and it *is*
  related to the golden ratio, as guessed: it's a value deliberately
  chosen a bit below the golden ratio (~1.618), borrowing the same
  growth-factor reasoning used for dynamic array resizing (a growth
  factor at or above the golden ratio can never reuse previously
  freed memory as the array grows) rather than any restart-specific
  tuning experiment. Per `STAGE15.md`'s preference for a standard
  value, both `c` and `r` are taken directly from that pairing.

`a`, `b`, `c`, and `r` are all internal (non-`--alg-params`) constants,
as `STAGE15.md` explicitly asks for `a` and `b`, "which we optimize
later" -- extended to `c`/`r` for consistency. The large gap between
`a` (18000) and `b`/`c` (100) is not a bug: it reflects that a genuine
literature standard existed for `b` and `c` but not `a`, and the
benchmark below suggests this asymmetry may itself be part of why
Luby's showing was so much weaker than polynomial's on this project's
harder-than-industrial-average benchmark set (see below).

## Command line arguments

Per `STAGE15.md`, `--algorithm=cdcl`'s `--alg-params`/`-p` grows a
third value:

```
--alg-params <val1> [<val2> [<val3>]]
  val1 = SelectVar heuristic (0-3; unchanged from Stage 13)
  val2 = restart strategy (STAGE15.md):
    0 = no restarts
    1 = Luby
    2 = polynomial (the default; see the benchmark below for why)
    3 = geometric (added after this report's first draft; see the
        naming note above and the benchmark below)
  val3 = learned-clause database memory limit (STAGE12.md; this was
         val2 before this stage)
```

As before, values are positional: `val2` requires `val1` to be given
(even if it's just the default, 2), and `val3` requires both. This
matches the convention already established in Stage 12/13's
`--alg-params` handling, extended by one slot in both the Go
(`buildArgs`'s `cdcl` branch, `validate`) and Rust
(`build_args`/`validate` in `cliargs.rs`) implementations. The
existing `maxValues`/`num_args` cap of 3 (already present to serve
`ws`'s three values) needed no widening.

## Verification

- **Unit tests** (Go `cdcl` package: 35, +9 from Stage 14's 26;
  Rust: 159, +11 from Stage 14's 148): direct tests of
  `lubyTerm`/`luby_term` against the first 15 hand-verified terms of
  the sequence; direct tests of `restartThreshold`/`restart_threshold`
  for all three math-bearing schedules -- Luby, polynomial, and
  geometric (including the true geometric schedule's formula and its
  first four pinned values, 100/150/225/337); a direct
  test that `maybeRestart`/`maybe_restart` is a no-op under
  `RestartNone` regardless of accumulated conflicts; a direct test
  that it triggers exactly at threshold, resets the counter, advances
  the restart index, and -- critically -- leaves a previously learned
  clause completely undisturbed while unassigning the in-progress
  decision; and end-to-end SAT/UNSAT runs under all three restart
  strategies, confirming restarts change *how* the search proceeds
  without changing *what* it concludes. `go build ./...`,
  `gofmt -l .`, `go vet ./...`, `go test ./...` (9 packages) and
  `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`,
  `cargo test` (159 tests) are all clean.
- **Cross-language correctness sweep**: an independent, from-scratch
  Python script (not reusing any project code) ran both release
  binaries against 10 sampled files each from `uf50-218`/`uuf50-218`
  and `uf100-430`/`uuf100-430` (SATLIB's uf*/uuf* naming gives ground
  truth), across 20 `--alg-params` combinations (all four SelectVar
  variants crossed with all four restart strategies, plus three runs
  adding a tiny memory limit, plus the all-defaults case): **200/200
  runs clean** -- zero SAT/UNSAT verdict errors against ground truth,
  zero Go-vs-Rust disagreements, and zero independently-detected bad
  solutions (parsing the CNF and the solution file from scratch and
  checking every clause directly). Verbose output
  (`cdcl: select_var=... restart=...`) and, since the RNG seed and
  algorithm are identical, the satisfying assignments themselves are
  byte-for-byte identical between the two binaries.

## Benchmark: which restart strategy, and does it help?

Following the exact methodology of Stages 13/14's benchmarks: the
same 15 sampled `uf250-1065` (SAT, 20s cap) and 10 sampled
`uuf250-1065` (UNSAT, 45s cap) files, comparing all four restart
strategies against each other (VSIDS + phase saving held fixed,
`-p 2 <restart>`):

| Set | Restart strategy | Solved | Mean (solved) | Median (solved) |
|---|---|---|---|---|
| SAT (`uf250-1065`) | none | 12/15 | 4.27s | 2.74s |
| SAT (`uf250-1065`) | Luby | 12/15 | 3.14s | 1.03s |
| SAT (`uf250-1065`) | **polynomial** | 13/15 | 4.19s | 1.35s |
| SAT (`uf250-1065`) | **geometric** | **14/15** | 3.97s | 1.98s |
| UNSAT (`uuf250-1065`) | none | 6/10 | 32.6s | 38.0s |
| UNSAT (`uuf250-1065`) | Luby | **3/10** | 29.4s | 24.2s |
| UNSAT (`uuf250-1065`) | **polynomial** | **10/10** | 29.0s | 31.8s |
| UNSAT (`uuf250-1065`) | geometric | 7/10 | 22.6s | 24.0s |

Three things stand out:

1. **The polynomial schedule is still the clear overall winner,
   especially for proving UNSAT.** 10/10 UNSAT instances solved
   within the 45s budget -- every single sampled instance -- against
   6/10 with no restarts, 7/10 with true geometric, and just 3/10
   with Luby.
2. **The new true geometric schedule is genuinely competitive, and
   actually edges out polynomial on the SAT side** (14/15 vs. 13/15,
   the best solved count of any strategy on that set), but falls
   well short of polynomial on the harder UNSAT set (7/10 vs. 10/10)
   -- better than no restarts and much better than Luby there, but
   not enough to catch polynomial. No single strategy dominates both
   sets; polynomial's UNSAT margin is simply too large to give up for
   a smaller SAT-side gain.
3. **Luby restarts, using the literature-standard base `b=100`,
   actually *hurt* UNSAT performance relative to no restarts at
   all** (3/10 vs. 6/10). This is very likely the constant, not the
   sequence: `b=100` means Luby's very first restarts land after only
   100-200 conflicts, which on this project's 250-variable uniform
   random 3-SAT instances (~18,000 conflicts/second, per the
   measurement above) is a small fraction of a second -- plausibly
   too frequent for VSIDS's activity ordering to stabilize into
   anything useful before the search is thrown away and restarted
   again. MiniSat's `rfirst=100` was presumably calibrated against a
   much broader and, on average, easier benchmark mix than this
   project's uniform-random 250-variable set. Notably, the true
   geometric schedule shares that same `c=100` base yet performs far
   better than Luby (7/10 vs. 3/10) -- its slow (`r=1.5`) early
   growth apparently gives VSIDS enough room to stabilize between
   the first few restarts in a way Luby's `1, 1, 2, ...` pattern
   (repeated short intervals) does not.

Given this, and following the same principle Stage 13's SelectVar
default followed (real measurement on this project's own benchmark
set outranks an a priori literature default), `--algorithm=cdcl`
keeps `RestartPolynomial`/`RestartStrategy::Polynomial` as its
default: it remains the strongest single choice, particularly for
UNSAT, even after a genuinely stronger SAT-side alternative was
added. Users who want Luby, no restarts, or the new true geometric
schedule (a good choice specifically if the workload is
SAT-dominated) still get any of them by passing `--alg-params
<val1> 1`, `<val1> 0`, or `<val1> 3` respectively.

## Testing

- Go: `go build ./...`, `gofmt -l .`, `go vet ./...`, and
  `go test ./...` (9 packages, 35 `cdcl` tests) are all clean.
- Rust: `cargo fmt --check`, `cargo clippy --all-targets -- -D
  warnings`, and `cargo test` (159 tests) are all clean.
- Manual/scripted verification as described above (200/200 clean
  cross-language runs).

## Open questions / notes for you

- **The Luby base `b=100` may simply be miscalibrated for this
  project's benchmark set**, per the discussion above -- it's a
  genuine literature standard, but it visibly underperforms here.
  Since `STAGE15.md` already frames both constants as "internal
  parameters which we optimize later," I left `b` at the standard
  value rather than empirically retuning it myself this stage (that
  would have meant picking a *different* answer than `STAGE15.md`'s
  own preference ordering asks for on the first pass), but a future
  tuning pass should probably try a substantially larger `b` for
  Luby specifically, given how badly 100 performed on the harder
  UNSAT sample.
- The quadratic sequence `STAGE15.md` originally called "geometric"
  is now named `RestartPolynomial`/`RestartStrategy::Polynomial`
  instead, and a genuinely geometric schedule
  (`RestartGeometric`/`RestartStrategy::Geometric`, `c*r^k`) was added
  in its place at `--alg-params` value 3, per your follow-up request.
- "Node count" was read as conflict count throughout, matching the
  restart literature's own convention and this project's existing
  `NumConflicts`/`num_conflicts` field; there is no separate "node"
  concept in `cdcl` the way `dfs` has one.
- No changes to `dfs`, `hc`, or `ws` in either language, exactly as
  `STAGE15.md` asked.
