# Report: Stage 53

## Summary

`STAGE53.md` originally asked for the progress meter / heartbeat
(`REPORT50.md`'s Tier 1 item 3). **That was deferred, not done this
stage**: you want the full multi-threaded-sync version (real
cross-thread aggregation via atomic counters, not per-thread lines or
a single-threaded-only version), which is a meaningfully bigger lift
than "cheap progress meter" implies — you've put it back on your own
todo list rather than scoping it down for now. Instead, this stage
re-runs `REPORT40.md`'s external comparison against `minisat` (2.2.1)
and `cryptominisat5` (5.8.0): the same 25 files, plus
`benchmark/uf250-1065/uf250-03.cnf` (`REPORT40.md`'s own addendum from
`STAGE52.md`, added after `REPORT51.md` found it regressed under the
`chooseWatch` scan-position fix) — 26 files total, current `vibe_sat`
code (through `STAGE52.md`).

**Headline finding: the gap to `minisat`/`cryptominisat5` has closed
substantially since `REPORT40.md`, and one previously-unsolved
industrial instance is now solved.** On the two 250-variable random
instances that showed the clearest gap in `REPORT40.md`:

- **`uf250-0100.cnf` (SAT)**: `vibe_sat` went from 3.14x/2.79x slower
  than `minisat` (Go/Rust) to **1.93x/1.83x** — most of the original gap
  is gone.
- **`uuf250-0100.cnf` (UNSAT)**: `vibe_sat` went from 8.68x/7.78x slower
  to **2.77x/2.66x** — a dramatic reduction, and Rust now runs within
  8% of `cryptominisat5`'s own time on this file (10.24s vs. 9.48s).
- **`huck.col.11.cnf`** (graph coloring): both `vibe_sat`
  implementations timed out in `REPORT40.md`; **both now solve it**
  (Go 44.39s, Rust 40.62s) — a qualitative win, not just a
  quantitative one, and Rust's own number is 30% faster than
  `REPORT47.md`'s intermediate measurement (58.38s, taken after only
  the Stage 45/46 fix).

Every other file's result is essentially unchanged from `REPORT40.md` —
expected, since nothing in Stages 41-52 targeted preprocessing depth or
a fundamentally different technique, just `propagate`/bootstrap
overhead. `uf250-03.cnf`, the new addition, shows the largest relative
gap of any file in the set (`vibe_sat` roughly 10x slower than
`minisat`) — this is `REPORT51.md`'s own already-disclosed per-file
regression under the `chooseWatch` fix showing up here too, not a new
finding.

## Method

Identical to `REPORT40.md` in every respect except the file count (26,
not 25) and which stage's binaries are being measured: same solvers and
settings (`vibe_sat --algorithm=cdcl --num-threads=1`, `minisat`
default, `cryptominisat5 -t 1 --verb 0`), same 60-second `timeout` cap
per solver per file, same sequential (never concurrent) execution, same
cleaned copies for the 12 SATLIB-format random files (trailing `%`/`0`
trailer stripped) and the 2 `ssa` files (tabs normalized to spaces) —
`minisat`/`cryptominisat5` only, `vibe_sat` ran the original files
directly, exactly as `REPORT40.md` established. `uf250-03.cnf` needed
the same SATLIB-trailer cleaning as the rest of its family. Both
`vibe_sat` binaries rebuilt fresh, optimized/release mode, from the
current tree (through `STAGE52.md`, uncommitted). Every file's clean
copy was parse-checked against both external solvers (a fast
`timeout 3` pass) before the real timed sweep, per `REPORT40.md`'s own
verification discipline — no parse errors found.

## Results

Wall-clock seconds, `TIMEOUT` = hit the 60-second cap. Every verdict
cross-checked programmatically: wherever two or more solvers reached a
definite verdict on the same file, they agreed — 0 mismatches across
all 26 files.

| File | go (cdcl) | rust (cdcl) | minisat | cryptominisat5 |
|---|---|---|---|---|
| uf20-01.cnf (SAT) | 0.005s | 0.005s | 0.007s | 0.011s |
| uf50-01000.cnf (SAT) | 0.005s | 0.006s | 0.007s | 0.011s |
| uf75-0100.cnf (SAT) | 0.009s | 0.008s | 0.008s | 0.011s |
| uf100-01000.cnf (SAT) | 0.009s | 0.007s | 0.008s | 0.014s |
| uf175-0100.cnf (SAT) | 0.078s | 0.065s | 0.013s | 0.036s |
| **uf250-0100.cnf (SAT)** | **5.212s** | **4.955s** | 2.703s | 3.354s |
| **uf250-03.cnf (SAT, new)** | **4.827s** | **4.624s** | 0.458s | 1.864s |
| uuf50-01000.cnf (UNSAT) | 0.004s | 0.005s | 0.006s | 0.011s |
| uuf75-0100.cnf (UNSAT) | 0.008s | 0.007s | 0.008s | 0.012s |
| uuf100-01000.cnf (UNSAT) | 0.012s | 0.010s | 0.010s | 0.015s |
| uuf175-0100.cnf (UNSAT) | 0.070s | 0.055s | 0.026s | 0.073s |
| **uuf250-0100.cnf (UNSAT)** | **10.683s** | **10.242s** | 3.855s | 9.477s |
| anomaly.cnf (SAT) | 0.005s | 0.005s | 0.007s | 0.011s |
| medium.cnf (SAT) | 0.010s | 0.008s | 0.007s | 0.011s |
| bw_large.a.cnf (SAT) | 0.022s | 0.019s | 0.010s | 0.013s |
| ssa0432-003.cnf (UNSAT) | 0.030s | 0.026s | 0.008s | 0.012s |
| ssa7552-038.cnf (SAT) | 0.401s | 0.232s | 0.008s | 0.009s |
| **huck.col.11.cnf** | **UNSAT 44.39s** | **UNSAT 40.62s** | UNSAT 33.87s | UNSAT 17.14s |
| a_rphp045_05.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| queen8_8.col.9.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| factoring29986577x29986577.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| sdiv15prop.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| Karatsuba7654321x1234567.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |
| prime_119218851371.cnf | TIMEOUT | TIMEOUT | TIMEOUT | UNSAT 48.91s |
| ortholatin-7.cnf | TIMEOUT | TIMEOUT | TIMEOUT | SAT 17.67s |
| apn-sbox5-cut3-symmbreak.cnf | TIMEOUT | TIMEOUT | TIMEOUT | TIMEOUT |

## Before / after (Stage 40 → Stage 53), the files that moved

| File | go: before → after | rust: before → after |
|---|---|---|
| uf250-0100.cnf (SAT) | 8.283s → 5.212s (**-37%**) | 7.358s → 4.955s (**-33%**) |
| uuf250-0100.cnf (UNSAT) | 33.99s → 10.68s (**-69%**) | 30.48s → 10.24s (**-66%**) |
| huck.col.11.cnf | TIMEOUT → **44.39s** | TIMEOUT → **40.62s** |

Ratio to `minisat` (the closest external competitor throughout):

| File | go/minisat before → after | rust/minisat before → after |
|---|---|---|
| uf250-0100.cnf (SAT) | 3.14x → **1.93x** | 2.79x → **1.83x** |
| uuf250-0100.cnf (UNSAT) | 8.68x → **2.77x** | 7.78x → **2.66x** |

This tracks directly onto the stages that ran in between: Stage 45/46's
watch-list rewrite, Stage 47/48's blocking-literal-check reversal, and
Stage 51's `chooseWatch` scan-position fix, all specifically targeting
`propagate`'s own cost — exactly the mechanism that dominates on
these two, larger, harder-to-propagate-through instances. Rust's
`uuf250-0100.cnf` time (10.24s) is now within 8% of `cryptominisat5`'s
own (9.48s) on this file — the closest `vibe_sat` has come to matching
a competition-grade solver anywhere in this comparison's history.

## `uf250-03.cnf`: the new file's own story

This file was added specifically because `REPORT51.md` found it
regressed under `STAGE51.md`'s `chooseWatch` fix (0.94s → 4.67s in that
report's own isolated measurement) — a real, disclosed instance of the
"changing watch-move order shifts CDCL's search trajectory, usually for
the better, occasionally not" phenomenon. That regression shows up
again here, more starkly, against external competition: `minisat`
solves it in 0.458s and `cryptominisat5` in 1.864s, while `vibe_sat`
takes 4.6-4.8s — a ~10x gap, the single worst ratio anywhere in this
26-file set. This isn't a new finding (it's the same file, same known
cause, just now visible against external solvers instead of only
against `vibe_sat`'s own before/after), but it's a useful, concrete
illustration of the tradeoff `REPORT51.md` already accepted: a large
net-positive change with real, occasional per-file cost.

## What's unchanged

Every other observation from `REPORT40.md` still holds: the 15 easy
files (everything except the four 250-variable/large-industrial cases
discussed above and `huck.col.11`) are all sub-second across all four
solvers and the ranking among them is noise, not signal;
`prime_119218851371.cnf`/`ortholatin-7.cnf` are still solved only by
`cryptominisat5` (consistent with its heavier built-in Gaussian
elimination/XOR extraction mattering specifically on these
algebraically-structured instances — the low-priority "XOR/Gaussian
elimination detection" item on your own list is exactly this);
`apn-sbox5-cut3-symmbreak.cnf` remains unsolved by every solver tested,
`minisat`/`cryptominisat5` included, at this time budget; Go and Rust
`vibe_sat` continue to track each other closely, Rust consistently a
little ahead on every measurably-slow case.

## Verification

- Every one of the 26 files (originals for `vibe_sat`, cleaned copies
  for `minisat`/`cryptominisat5` where needed) was parse-checked
  against both external solvers before the timed sweep — no parse
  errors.
- Every verdict was cross-checked programmatically against every other
  solver's verdict on the same file: 0 mismatches across all 26 files.
- Both `vibe_sat` binaries built fresh in optimized/release mode
  (`go build`; `cargo build --release`) from the current tree.
- All 104 runs (26 files x 4 solvers) executed sequentially, one at a
  time, on an otherwise idle machine.
- No code changes this stage — a pure measurement/reporting exercise,
  like `REPORT40.md`/`REPORT47.md` before it.

## Documentation

No changes — a comparison re-run doesn't add a new CLI flag, algorithm,
or technique. Scratch files (the 26-file working copies, cleaned
copies, the comparison script, raw timing output) lived entirely
outside the repository and were not committed.

## Questions for you

- `STAGE53.md`'s original ask (the progress meter) is still on your
  own todo list per your own note — nothing further needed from me on
  it until you bring it back up.
- The gap on `uf250-0100.cnf`/`uuf250-0100.cnf` has closed enough that
  the remaining ~1.8-2.8x isn't obviously attackable by another
  `propagate`-side fix the way the last four stages' worth of work
  was — is closing it further still a goal, or is "closed most of the
  way, remaining gap likely needs a different kind of technique
  (better branching/restart tuning, or something structural like
  chronological backtracking)" a fine place to leave this for now?
- `uf250-03.cnf`'s ~10x gap is a known, already-accepted regression,
  not a new bug — anything you want done about it specifically, or is
  recording it here (now visible against external solvers, not just
  internally) enough?
