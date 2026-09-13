# Report: Stage 10

## Summary

Stage 10 asked for a comparison, not a change: run the Go and Rust
`vibe_sat` binaries with `--algorithm dfs --alg-params 0` (the default,
clause-weighted `SelectVar` heuristic) against the new 250-variable
SATLIB benchmarks and see which is currently faster. No solver code was
touched this stage. One correction to the stage description:
`STAGE10.md` refers to `benchmark/uuf205-1065`, but the directory that
was actually added is `benchmark/uuf250-1065` (205 looks like a typo
for 250, matching `uf250-1065`'s naming); this report uses the
directory that exists.

**Bottom line: the Rust binary is substantially faster than the Go
binary on both benchmark sets — roughly 2x on solved instances, and
Rust also solves more instances than Go within the same time budget.**

## Methodology

`benchmark/uf250-1065` and `benchmark/uuf250-1065` each contain 100
instances. A handful of manual single-file timings (both SAT and
UNSAT) showed individual runs ranging from a few seconds up to a
minute or more with this heuristic on 250-variable formulas — running
all 200 files to completion with no time limit was impractical within
this session, so I instead ran capped, sampled sweeps and measured
both *how many instances each binary solves* and *how fast* within the
cap, which is enough to answer "which one is faster":

- **SAT set**: first 40 files of `benchmark/uf250-1065` (alphabetical
  order), 30-second cap per run.
- **UNSAT set**: first 25 files of `benchmark/uuf250-1065`, 60-second
  cap per run (UNSAT instances are harder here: proving unsatisfiability
  requires exhausting the search space, whereas a SAT instance can
  finish as soon as one solution is found).
- Both binaries built in release/optimized mode (`go build` default;
  `cargo build --release`), run with `-a dfs -p 0 --time-limit-secs
  <cap> -v 1`, and timed end-to-end (process start to exit) via wall
  clock. Both sweeps ran on the same machine; each binary's sweep ran
  in its own process, and the machine has 10 cores, so the two
  single-threaded sweeps did not meaningfully contend with each other
  even where they overlapped in time.
- A run that hits the cap is reported by the solver itself as
  `UNKNOWN` (per Stage 5's timeout handling) and is counted as *not
  solved* rather than timed at the cap value, since the cap value
  reflects the time limit, not the instance's actual difficulty.

## Results

| Set | Binary | Instances tried | Solved within cap | Mean time (solved only) | Median time (solved only) |
|---|---|---|---|---|---|
| SAT (`uf250-1065`, cap 30s) | Go | 40 | 28 | 8.31s | 6.01s |
| SAT (`uf250-1065`, cap 30s) | Rust | 40 | 38 | 6.39s | 3.17s |
| UNSAT (`uuf250-1065`, cap 60s) | Go | 25 | 15 | 43.47s | 44.50s |
| UNSAT (`uuf250-1065`, cap 60s) | Rust | 25 | 25 | 19.61s | 18.78s |

Rust solves **10 more of the 40** sampled SAT instances (38 vs 28) and
**all 25** of the sampled UNSAT instances within the cap, where Go
times out on 10 of them (15/25). Where both binaries do solve the same
instance, Rust is consistently faster — roughly 1.3x on the SAT sample
(median) and roughly 2.4x on the UNSAT sample (median) — and the gap
compounds into Go simply running out of time on a meaningful fraction
of instances that Rust dispatches with room to spare.

## Correctness cross-check

Before trusting a speed comparison, I confirmed the two binaries still
agree: of the instances **both** binaries solved within their
respective caps (43 across both sets), all 43 verdicts matched exactly
(all `SAT` where expected, all `UNSAT` where expected) — 0 mismatches.
This is the same watched-literals `dfs` code validated in
`REPORT9.md`; Stage 10 is a pure performance observation on top of it,
not a new correctness question, but re-confirming it here at a larger
problem size seemed worthwhile before drawing any speed conclusions
from the data.

## Discussion

`STAGE9.md`'s own framing (watched literals as "the engineering
foundation that makes everything below it... practical at scale") is
visibly paying off here — these are the first benchmarks in the
project big enough (250 variables, ~1065 clauses) that per-node BCP
cost, not just node count, meaningfully separates the two
implementations. I did not dig into *why* Rust pulls ahead by this
much (that would mean profiling both binaries, which felt like it
belonged to a future stage focused on optimization rather than this
one's benchmarking ask), but the shape of the gap — worse on the
harder, more BCP-heavy UNSAT set than on the easier SAT set — is at
least consistent with per-node work (allocation, bounds checking,
copying) being the dominant cost difference between the two languages'
implementations, rather than, say, a difference in the number of
search nodes explored (which the algorithm guarantees are identical
between the two, per `REPORT9.md`'s node-count-equivalence check).

## Command line arguments

None — this stage made no code or CLI changes in either language.

## Testing

No code changed, so no new tests were added; existing `go test ./...`
and `cargo test` suites were not affected and were not re-run beyond
what Stage 9 already verified. The correctness cross-check above
serves as this stage's verification, since it's the only new claim
being made (relative performance) that could otherwise be undermined
by an undetected correctness regression.

## Open questions / notes for you

- The full 200-file benchmark sets were not run to exhaustion — the
  sampled, capped sweep (40 SAT + 25 UNSAT files) was chosen to keep
  this stage's runtime reasonable while still producing a clear,
  consistent signal. If you'd like the full 100+100 sweep for a more
  precise number, that's straightforward to run as a longer background
  job, but I don't expect the conclusion ("Rust is faster, by roughly
  2x on this heuristic/size") to change.
- I did not investigate the *cause* of the gap (no profiling was done
  in either language) — flagging in case narrowing that down is
  something you'd want as a follow-up, independent of whatever Stage
  11 turns out to be.
- `benchmark/uf250-1065`, `benchmark/uuf250-1065`, and the corresponding
  `benchmark/tarfiles/*.tar.gz` are currently untracked in git (not
  committed) — I left them as you added them rather than staging
  anything, per the project's standing convention that you review and
  commit.
