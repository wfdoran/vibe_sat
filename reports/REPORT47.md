# Report: Stage 47

## Summary

`STAGE47.md` asked to re-run `REPORT40.md`'s exact comparison — same
25 files, same 60-second cap, `cdcl` only this time — to see how much
of the original `minisat`/`cryptominisat5` gap the Stage 45/46
watch-list fixes actually closed.

**The gap closed substantially on large random 3-SAT, and one
previously-unsolved industrial instance is now solved.** On the
250-variable random set (the exact instance class `REPORT40.md`
measured the clearest gap on):

- **SAT instance**: `vibe_sat` went from 3.1-3.4x slower than `minisat`
  to 1.1-1.3x slower — most of the original gap is gone.
- **UNSAT instance**: `vibe_sat` went from 7.8-8.7x slower to 4.2-5.3x
  slower — a real, large improvement, but a genuine gap remains.
- **`huck.col.11`** (graph coloring): both `vibe_sat` implementations
  timed out in `REPORT40.md`; **Rust now solves it** (58.4s, just
  under the cap) — a qualitative win, not just a quantitative one.

Every other file's result is essentially unchanged (small-instance
noise, or still a timeout for both `vibe_sat` and the external
solvers on the harder industrial instances) — exactly what you'd
expect from a fix that specifically targets `propagate`'s own
overhead, not preprocessing depth or a fundamentally different
solving technique.

## Method

Identical to `REPORT40.md` in every respect except which stage's
binaries are being measured: same 25 files (11 random 3-SAT, 5
structured, 9 industrial — see `REPORT40.md` for the full list and
selection rationale), same cleaned copies for `minisat`/
`cryptominisat5` (SATLIB trailer stripped, `ssa` files' tabs
normalized), same 60-second `timeout` cap per solver per file, same
sequential (never concurrent) execution, `minisat`/`cryptominisat5`
default settings, `vibe_sat --algorithm=cdcl` single-threaded. Both
`vibe_sat` binaries rebuilt fresh from the current tree (Stage 46's
`dfs` fix included, though irrelevant here since only `cdcl` is
tested).

One environment note, not a methodology change: this session's
backgrounded-process monitor repeatedly killed the sweep with a false
"low memory" report on longer-running batches (the same intermittent,
non-reproducible issue flagged in `REPORT39.md`/`REPORT42.md`) —
worked around by resuming the sweep in smaller batches (down to one
file at a time for the three slowest remaining files) rather than
re-running the whole thing, with no effect on the results themselves.

## Results

Wall-clock seconds, `TIMEOUT` = hit the 60-second cap. Every verdict
cross-checked: wherever two or more solvers reached a definite verdict
on the same file, they agreed.

| File | go (cdcl) | rust (cdcl) | minisat | cryptominisat5 |
|---|---|---|---|---|
| uf20-01.cnf (SAT) | 0.008s | 0.006s | 0.009s | 0.013s |
| uf50-01000.cnf (SAT) | 0.007s | 0.006s | 0.008s | 0.011s |
| uf75-0100.cnf (SAT) | 0.009s | 0.007s | 0.008s | 0.012s |
| uf100-01000.cnf (SAT) | 0.008s | 0.008s | 0.008s | 0.013s |
| uf175-0100.cnf (SAT) | 0.090s | 0.082s | 0.014s | 0.025s |
| **uf250-0100.cnf (SAT)** | **3.205s** | **2.719s** | 2.547s | 3.144s |
| uuf50-01000.cnf (UNSAT) | 0.005s | 0.004s | 0.007s | 0.011s |
| uuf75-0100.cnf (UNSAT) | 0.008s | 0.007s | 0.007s | 0.012s |
| uuf100-01000.cnf (UNSAT) | 0.015s | 0.013s | 0.010s | 0.015s |
| uuf175-0100.cnf (UNSAT) | 0.084s | 0.073s | 0.025s | 0.076s |
| **uuf250-0100.cnf (UNSAT)** | **20.59s** | **16.32s** | 3.907s | 9.454s |
| anomaly.cnf (SAT) | 0.005s | 0.004s | 0.007s | 0.011s |
| medium.cnf (SAT) | 0.012s | 0.009s | 0.008s | 0.013s |
| bw_large.a.cnf (SAT) | 0.026s | 0.018s | 0.011s | 0.012s |
| ssa0432-003.cnf (UNSAT) | 0.029s | 0.025s | 0.008s | 0.012s |
| ssa7552-038.cnf (SAT) | 0.366s | 0.221s | 0.009s | 0.013s |
| **huck.col.11.cnf** | TIMEOUT | **UNSAT 58.38s** | UNSAT 34.49s | UNSAT 17.42s |
| a_rphp045_05.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| queen8_8.col.9.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| factoring29986577x29986577.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| sdiv15prop.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| Karatsuba7654321x1234567.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| prime_119218851371.cnf | TIMEOUT | TIMEOUT | TIMEOUT | UNSAT 50.37s |
| ortholatin-7.cnf | TIMEOUT | TIMEOUT | TIMEOUT | SAT 17.77s |
| apn-sbox5-cut3-symmbreak.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |

## Before / after (Stage 40 → Stage 47), the files that actually moved

| File | go: before → after | rust: before → after |
|---|---|---|
| uf250-0100.cnf (SAT) | 8.283s → 3.205s (**-61%**) | 7.358s → 2.719s (**-63%**) |
| uuf250-0100.cnf (UNSAT) | 33.99s → 20.59s (**-39%**) | 30.48s → 16.32s (**-46%**) |
| huck.col.11.cnf | TIMEOUT → TIMEOUT | TIMEOUT → **UNSAT 58.38s** |

Ratio to `minisat` (the closest external competitor throughout), on
the two random instances where a gap existed:

| File | go/minisat before → after | rust/minisat before → after |
|---|---|---|
| uf250-0100.cnf (SAT) | 3.14x → **1.26x** | 2.79x → **1.07x** |
| uuf250-0100.cnf (UNSAT) | 8.68x → **5.27x** | 7.78x → **4.18x** |

Every other file's numbers are within normal run-to-run noise of
`REPORT40.md`'s own — no regression anywhere, and nothing else moved
enough to matter.

## What this does and doesn't tell us

- The Stage 45/46 fix's benefit scales with instance size, exactly as
  the underlying diagnosis predicted (`O(occurrences)` vs.
  `O(current watchers)` — the gap between them grows with how many
  clauses a typical variable appears in, which grows with instance
  size for this random 3-SAT family). The 20-175 variable instances
  were already fast enough that the difference is lost in noise; 250
  variables is where it starts to show clearly.
- **The UNSAT side still has a real, unclosed gap** (4.2-5.3x) even
  after this fix — proving unsatisfiability exhaustively still costs
  meaningfully more than `minisat`'s decades of additional tuning
  account for, on top of whatever `propagate`'s own remaining cost is.
- The industrial instances didn't move at all, which is expected, not
  a sign the fix didn't work: `huck.col.11` aside, none of the other 8
  were close to the 60-second boundary in `REPORT40.md`'s own numbers
  (they were all comfortably-timed-out, not "almost solved") — a
  ~20-60% speedup on `propagate` alone was never going to be enough to
  cross that gap for these; that would need either a longer time
  budget or a different lever entirely (deeper preprocessing, better
  branching, or the specific missing capabilities `REPORT40.md`
  already identified, e.g. `cryptominisat5`'s XOR/Gaussian elimination
  for `ortholatin-7`/`prime_119218851371`).

## What I'd push to the top of the list, if asked

Given your stated focus is `cdcl` specifically: **a fresh CPU profile
on the current, post-Stage-45/46 code** is the most informationally
valuable next step before picking a specific optimization target,
since the last profile (`REPORT41.md`, pre-fix) is now stale —
`propagate` no longer dominates at 97%+; `chooseWatch`'s own linear
scan for a replacement watch was already 52.91% of *post-fix* time in
the last profile taken right after Stage 45's change (see
`REPORT45.md`), and that's the more actionable candidate now, not
`propagate`'s own candidate-discovery cost, which this stage's fix
already addressed. I'd want a current number before recommending
anything more specific than "look at `chooseWatch` next" — this is
exactly the kind of claim this project's own culture says to measure
rather than assume.

Beyond that, in rough priority order for closing the remaining
`uuf250`-style UNSAT gap specifically: (1) re-profile first, as above;
(2) if `chooseWatch` is still the leading cost, look at whether its
linear scan itself can be cheaper (e.g. remembering a scan position
across calls rather than restarting from the clause's first literal
every time — a real, standard MiniSat-lineage technique this project
hasn't tried); (3) the parameter-tuning avenue (`REPORT42.md`) came up
empty before, but that was tuning against the *old*, slower
`propagate` — worth knowing if that conclusion still holds, though I'd
rank a fresh profile above re-tuning blind.

## Verification

No code changes this stage — a pure measurement re-run. Both
`vibe_sat` binaries built fresh (`go build`, `cargo build --release`)
from the current tree; every verdict cross-checked against every
other solver's verdict on the same file (no disagreement anywhere,
matching `REPORT40.md`'s own standard). Scratch files (the 25-file
working copies, cleaned trailer-stripped copies, the comparison
script) lived entirely outside the repository and were removed after.

## Documentation

No changes — a comparison re-run doesn't add a new CLI flag,
algorithm, or technique.
