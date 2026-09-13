# Report: Stage 11

## Summary

Stage 11 is complete for both the Go and Rust versions of `vibe_sat`:
a new algorithm, `cdcl`, implements conflict-driven clause learning
with non-chronological backtracking (Marques-Silva & Sakallah,
"GRASP: A Search Algorithm for Propositional Satisfiability," IEEE
Trans. Computers, 1999; Moskewicz, Madigan, Zhao, Zhang & Malik,
"Chaff: Engineering an Efficient SAT Solver," DAC 2001), selectable
via `--algorithm=cdcl`/`-a cdcl`. Per `STAGE11.md`, `dfs` is untouched
(not a single line in `internal/dfs`/`dfs.rs` was changed), and `cdcl`
accepts the same single `--alg-params` value as `dfs` (0 = the
weighted `SelectVar` heuristic, 1 = the cheap static-order one) and
nothing else.

The headline result from the requested benchmark, though, is not the
one I expected going in: **on this project's benchmark set (uniform
random 3-SAT), "bare" CDCL — no VSIDS, no restarts, no clause
deletion, exactly what `STAGE11.md` asked for and nothing more —
solves *fewer* instances than `dfs` within the same time budget**,
despite being unambiguously correct (extensively cross-checked below)
and despite being noticeably *faster* than `dfs` on the instances it
does solve. See "Benchmark" below for the full story; this is a real,
diagnosed, and (per the SAT literature) well-precedented finding, not
a bug.

## Design

### Persistent trail + watched literals, resolving Stage 9's open question

`REPORT9.md`'s "open questions" section flagged a design tension:
Stage 9 kept `dfs`'s per-branch-cloned-assignment architecture (from
Stage 5) rather than restructuring around a single persistent,
incrementally-backtracked trail, and noted that "if Stage 10+ moves to
CDCL (which typically *wants* a persistent trail anyway...), this may
be worth revisiting." That's exactly what happened: `cdcl` is built
around one persistent assignment trail with real, in-place
backtracking, which is what non-chronological backtracking actually
requires (jumping back multiple decision levels at once isn't
expressible as "pop one stack entry" the way `dfs`'s single-level
backtrack is).

This is also precisely the setting watched literals were designed
for: a watch remains valid as long as it isn't watching a literal
that's currently false, and backtracking only ever turns an assigned
literal back into an unassigned one — never the other way around — so
every watch already in place is still legal after backtracking, with
**nothing to explicitly undo or reclone**. `backtrackTo`/`backtrack_to`
just truncates the trail and restores the assignment array; the watch
array is never touched. (Go: `go_src/internal/cdcl/cdcl.go`; Rust:
`rust_src/src/cdcl.rs`.)

### First-UIP conflict analysis

When `propagate`/`propagate` (the same watched-literal BCP as
`STAGE9.md`'s, adapted to run against the persistent trail's
propagation queue rather than a single branch) finds a clause fully
falsified, `analyze` walks the implication graph backward: starting
from the conflicting clause, it repeatedly resolves away the
literal most recently forced at the *current* decision level,
substituting in that literal's own reason clause, until exactly one
literal at the current level remains — the first unique implication
point (UIP). That literal's negation becomes the learned clause's
*asserting* literal; every other literal collected along the way is
already false at some lower level, which is exactly what makes the
learned clause a unit clause the moment the search backjumps to the
highest level among those (0 if there are none).

Level-0 literals are dropped from consideration entirely (they are
permanent facts that never need an antecedent recorded), and a defensive
check panics/`unreachable!`s if the walk ever tries to resolve through
a variable with no reason (a decision) while literals of the current
level remain unresolved — provably impossible given the trail's
structure (a decision variable is always the earliest entry of its
level, so it's always the *last* one the backward scan can reach), but
worth asserting explicitly given how easy this class of algorithm is
to get subtly wrong.

I verified this by hand before trusting it: `TestAnalyzeDerivesUnitClauseIndependentOfDecision`/
`test_analyze_derives_unit_clause_independent_of_decision` traces a
small 4-variable formula where I computed, on paper, that clauses
`{-2,4}` and `{-2,-4}` alone force `x2 = False` unconditionally — and
confirmed that `analyze` derives exactly that unit clause (backtrack
level 0), *without* ever inspecting the decision (`x1 = False`) that
happened to trigger the conflict, even though the decision is what
made the conflict reachable in the first place. This is the clearest
demonstration in this project so far of what non-chronological
backtracking actually buys you: the learned fact is strictly more
general than "the last decision was wrong."

### A unit-length learned clause needs no watches

A learned clause of length 1 has nowhere to put a second watch, and
needs none: `analyze`'s backtrack level for a unit clause is always 0,
so its asserting literal is about to become a permanent level-0 fact —
exactly like a variable fixed by the bootstrap unit propagation below.
`addLearnedClause`/`add_learned_clause` special-cases this: a
length-1 learned clause is never added to the clause database or
occurrence lists at all, since it has no further use once its
literal is asserted.

For length-≥2 learned clauses, the asserting literal is watched
directly (it's about to become true regardless of whether it's
currently "false" in the watched-literals sense), and the second watch
is whichever other literal has the *highest* decision level — that's
the one that will become unassigned soonest on some future backtrack,
keeping the watch valid the longest before it needs to shed anywhere.

### Bootstrap unit propagation, decision polarity, and duplicated helpers

`cdcl` bootstraps exactly like `dfs.Run` does (per `STAGE9.md`): one
defensive round of `preprocess.UnitPropagate`/`preprocess::unit_propagate`
before ever building watch state, since Stage 8's preprocessing
already does this by default but `--no-preprocessing` can bypass it,
and watched literals require every clause to have at least two
literals.

Every decision always tries `False` first, with no phase-saving —
consistent with `dfs`'s branch order, but chosen mostly for simplicity
rather than because it's known to be good; CDCL doesn't get to try
both polarities at one decision level the way `dfs`'s explicit
two-branch loop does, so if `False` is wrong, conflict analysis (not a
second branch attempt) is what corrects it.

Per `STAGE11.md` ("We will leave dfs as it is"), `internal/dfs`/`dfs.rs`
were not modified at all. `cdcl` reuses `dfs.SelectVar`/
`dfs.SelectVarFastPick` (`crate::dfs::select_var`/`select_var_fast_pick`
in Rust) directly, since those were already exported, but duplicates
the small watched-literal primitives (`chooseWatch`/`isFalse`,
`choose_watch`/`is_false`) rather than exporting them from `dfs`,
since the old `BCP`/`bcp` in `dfs`/`dfs.rs` still declares its own
identically-named private helpers and STAGE11.md asked that file be
left untouched.

One structural note: Rust's `cdcl.rs` is written as free functions
operating on loose local variables inside `run`, rather than a struct
with methods (unlike the Go version's `solver` struct). This sidesteps
Rust's borrow checker entirely — every piece of state is a genuinely
separate local binding, so passing disjoint pieces of it (some
mutable, some not) into helper functions is always straightforward,
where a `&mut self` method threading the same fields through a shared
struct would require the borrow checker to prove `lists` (borrowed
immutably) and `watch`/`x`/`trail` (borrowed mutably) never alias
across a method call, which it generally cannot do. `propagate` and
`analyze` end up with more parameters than `clippy` likes by default
(9 and 8, against its 7 threshold); I added `#[allow(clippy::too_many_arguments)]`
with a comment explaining why, rather than reintroducing the aliasing
problem by bundling them into a struct just to satisfy the lint.

## STAGE11.md's questions

**Are the added conflict clauses used as part of `SelectVar`?** Yes,
for the weighted variant (`--alg-params 0`, the default): every
decision rebuilds a `*cnf.Problem`/`Problem` wrapping the *current*
clause list (which grows every time a non-unit clause is learned) and
hands it to `dfs.SelectVar`/`select_var`, which scores every
not-yet-satisfied clause it's given — learned clauses included. The
fast variant (`--alg-params 1`) ignores clause contents entirely
either way, so learned clauses have no effect on it. This wasn't a
deliberate feature so much as the natural consequence of reusing
`dfs`'s existing heuristics against a clause list that happens to
grow; see "Benchmark" below for why this ended up mattering more than
I expected, in the wrong direction.

**Is there a limit on how many conflict clauses can be retained? Do
we need the user to pass some memory limit?** No limit was
implemented — clause retention is deliberately unbounded, since
`STAGE11.md` didn't ask for a deletion/reduction policy and one wasn't
obviously in scope for "CDCL + non-chronological backtracking" as a
single stage. I did not add a CLI flag for this. Going in, I expected
to report this as a purely theoretical future concern. Having run the
benchmark below, I no longer think it is: I measured conflict
throughput dropping from ~9,000 conflicts/sec to ~3,200 conflicts/sec
over a single 45-second run against one hard instance, entirely
consistent with a growing, never-pruned clause database making every
subsequent propagation step incrementally more expensive. **I'd now
recommend a clause database reduction policy (activity- or LBD-based,
the two standard choices) as the most valuable next addition to
`cdcl`, ahead of anything else** — see "Open questions" below.

## Verification

Given this is the most complex algorithm this project has implemented
so far, I verified it more heavily than usual, mirroring the rigor
`REPORT8.md` and `REPORT9.md` applied to their own trickiest pieces:

- **Unit tests** (13 per language, mirrored): watch-primitive tests
  (`chooseWatch`/`choose_watch`, `isFalse`/`is_false`), a bootstrap
  contradiction test, a unit-chain propagation test, the hand-traced
  first-UIP analysis test described above, both
  `addLearnedClause`/`add_learned_clause` branches (unit-clause
  skip; length-≥2 watch placement), and `Run`/`run`-level tests
  (satisfiable formula across both `SelectVar` variants, the same
  hand-verified unsatisfiable formula, the 4-pigeon/3-hole
  pigeonhole problem, zero-variable problems, and time-limit
  handling). `go test ./...` (9 packages) and `cargo test` (130
  tests) both pass cleanly.
- **`go vet`, `gofmt -l`, `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`**:
  all clean.
- **SAT/UNSAT ground-truth cross-check**: ran the built CLI binaries
  against 30 `uf20-91` (SAT) and 30 `uuf50-218` (UNSAT) files, both
  `SelectVar` variants: **60/60 correct for both Go and Rust.**
- **Independent solution verification**: a from-scratch Python script
  (parses the DIMACS file itself, never reusing solver code) checked
  every returned satisfying assignment against every original clause,
  across 25 `uf50-218` files x 4 flag combinations (both `SelectVar`
  variants x with/without `--no-preprocessing`): **0 violations for
  either language.**
- **`dfs`-vs-`cdcl` agreement**: since both are complete solvers over
  the same problems, they must agree on every verdict. Checked 40
  `uf75-325`/`uuf75-325` files x 4 flag combinations (160 runs) within
  each language: **0 disagreements.**
- **Go-vs-Rust parity**: the same 160-run sweep, comparing the two
  languages' `cdcl` directly against each other: **0 mismatches.**

None of this found a single correctness issue in `cdcl` itself; every
divergence from `dfs` described below is a **speed** difference on
instances both algorithms ultimately agree about (or, for the timed-out
ones, that `dfs` resolves within the same budget and `cdcl` does not).

## Benchmark

Per `STAGE11.md`: "When you get done, rerun the uf250-1065 and
uuf250-1065. This should give a good comparison between cdcl and dfs."
I reused Stage 10's exact methodology and data for `dfs` (same 40
sampled `uf250-1065` files at a 30s cap, same 25 sampled
`uuf250-1065` files at a 60s cap, `--alg-params 0`) and ran the
identical sweep against `cdcl`:

| Set | Algorithm | Binary | Solved / tried | Mean time (solved) | Median time (solved) |
|---|---|---|---|---|---|
| SAT (`uf250-1065`, cap 30s) | dfs | Go | 28 / 40 | 8.31s | 6.01s |
| SAT (`uf250-1065`, cap 30s) | dfs | Rust | 38 / 40 | 6.39s | 3.17s |
| SAT (`uf250-1065`, cap 30s) | **cdcl** | Go | **15 / 40** | 7.67s | **1.25s** |
| SAT (`uf250-1065`, cap 30s) | **cdcl** | Rust | **18 / 40** | 6.87s | **0.88s** |
| UNSAT (`uuf250-1065`, cap 60s) | dfs | Go | 15 / 25 | 43.47s | 44.50s |
| UNSAT (`uuf250-1065`, cap 60s) | dfs | Rust | 25 / 25 | 19.61s | 18.78s |
| UNSAT (`uuf250-1065`, cap 60s) | **cdcl** | Go | **0 / 25** | -- | -- |
| UNSAT (`uuf250-1065`, cap 60s) | **cdcl** | Rust | **0 / 25** | -- | -- |

Two things are true at once here, and both matter:

1. **On the SAT instances `cdcl` *does* solve, it is dramatically
   faster than `dfs`** — roughly 4.8x faster at the median in Go
   (1.25s vs 6.01s), roughly 3.6x faster in Rust (0.88s vs 3.17s).
   Conflict-driven learning is clearly paying for itself on the
   "easier" end of this instance distribution.
2. **`cdcl` solves visibly fewer instances than `dfs` within the same
   time budget overall** — roughly half as many SAT instances, and
   *zero* of the 25 sampled UNSAT instances (`dfs` solved 15-25 of
   them in the same 60 seconds). This is a heavy-tailed runtime
   distribution: a substantial fraction of instances (most
   pronounced on UNSAT) trigger a conflict count so large that the
   search blows straight through the time budget, dragging the
   solved-count and mean down even though the *typical* solved
   instance is fast.

I dug into *why*, rather than just reporting the numbers, using a
throwaway diagnostic harness (not committed) that ran `cdcl.Run`
directly against one of the UNSAT instances at increasing time caps
and reported the running conflict count:

```
cap=5s   conflicts=47,975   rate=9,006/s
cap=15s  conflicts=85,756   rate=5,684/s
cap=30s  conflicts=120,086  rate=3,939/s
cap=45s  conflicts=144,782  rate=3,216/s
```

Two separate effects compound here, both direct, known consequences
of implementing exactly what `STAGE11.md` asked for and nothing more:

- **No VSIDS, no restarts.** `cdcl` reuses `dfs`'s existing
  `SelectVar` heuristics unchanged — neither one is conflict-aware
  (no activity bumping toward variables that keep showing up in
  recent conflicts, which is what VSIDS is for), and there is no
  restart policy to abandon an unlucky decision sequence and retry
  with the learned clauses accumulated so far. Without either, the
  search needs a *very* large number of conflicts to resolve a hard
  instance — tens to hundreds of thousands, per the trace above,
  and still climbing with no sign of finishing at 45 seconds.
- **Unbounded clause retention** (the exact risk `STAGE11.md`'s
  second question anticipates): every one of those conflicts (except
  the rare unit-clause one) adds a clause to the database
  permanently, growing the occurrence lists every candidate-clause
  scan has to filter through. The conflict rate above drops by
  roughly 3x over 40 seconds on the same instance — direct evidence
  that the growing, never-pruned clause database is making each
  subsequent conflict progressively more expensive, on top of simply
  needing more of them in the first place.

This is not a surprising result in the SAT-solving literature — VSIDS
and restarts are widely considered essential companions to CDCL,
specifically because "bare" first-UIP learning without them is known
to sometimes underperform even a simple DPLL search on structureless
instances. This project's benchmark set (uniform random 3-SAT near the
satisfiability threshold) is exactly that kind of instance:
`REPORT8.md` already found that these formulas have almost no
exploitable structure for preprocessing to find; here, they similarly
give CDCL's core mechanism little to work with in the *absence* of a
heuristic that specifically hunts for that structure across conflicts
(VSIDS) or a way to bail out of an unlucky search path (restarts).

I'm reporting this plainly rather than only showcasing the favorable
median-time numbers: `cdcl`, as specified by `STAGE11.md`, is not
currently a strict improvement over `dfs` on this project's benchmark
set — it is a genuine trade, faster on the easy end and much worse on
the hard end, and the two missing standard companions (not "bugs," but
scope `STAGE11.md` didn't ask for) are the well-understood, empirically
confirmed reason why.

## Command line arguments

`--algorithm=cdcl`/`-a cdcl` selects the new algorithm. It accepts the
same single optional `--alg-params`/`-p` value as `dfs` (0 or 1,
selecting the `SelectVar` variant; defaults to 0) and no others, per
`STAGE11.md`. `--time-limit-secs`/`-t` and `--no-preprocessing`/`-x`
work exactly as they do for `dfs`. Help text and the CLI validation
switch in both languages were updated to include `cdcl` alongside
`hc`/`ws`/`dfs`.

## Testing

- Go: `go build ./...`, `gofmt -l .`, `go vet ./...`, and
  `go test ./...` (9 packages) are all clean.
- Rust: `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`,
  and `cargo test` (130 tests) are all clean.
- Manual/scripted verification as described above.

## Open questions / notes for you

- **My top recommendation for next steps on `cdcl` specifically**: a
  clause database reduction policy (delete low-activity or high-LBD
  learned clauses periodically) is, per the diagnostic evidence
  above, no longer a theoretical nice-to-have — it's the most direct
  lever on the measured throughput degradation. A VSIDS-style
  decision heuristic and a restart policy would likely matter even
  more for the solved-count headline metric, but are bigger, more
  invasive changes (VSIDS in particular is usually implemented as a
  replacement for `SelectVar` entirely, which would mean `cdcl` no
  longer shares `dfs`'s heuristics at all).
- I did not implement a `--alg-params`/memory-limit flag for clause
  retention, per `STAGE11.md`'s explicit "Initially, the only
  --alg-params will be the choice of SelectVar" — happy to add one if
  a deletion policy gets implemented and needs a knob.
- The decision polarity (always `False` first, no phase-saving) was a
  simplicity choice, not a considered one; if `cdcl` is revisited,
  phase-saving (remembering each variable's last assigned value across
  backtracks and preferring it next time) is a small, well-known,
  low-risk improvement worth trying before anything bigger.
- No language/toolchain version changes needed.
