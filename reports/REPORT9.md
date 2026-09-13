# Report: Stage 9

## Summary

Stage 9 is complete for both the Go and Rust versions of `vibe_sat`:
`dfs`'s boolean constraint propagation (BCP) now uses watched literals
(Moskewicz, Madigan, Zhao, Zhang & Malik, "Chaff: Engineering an
Efficient SAT Solver," DAC 2001) instead of rescanning occurrence
lists, per `STAGE9.md`. As specified, this is purely an internal
data-structure upgrade — there is no new CLI flag, no toggle, and the
watched-literal version is now the only `BCP`/`bcp`.

## Design

### The architectural tension, and how it's resolved

The textbook watched-literals design assumes a single, persistent
assignment trail with incremental backtracking: a variable's watches
never need to be "unwatched" on backtrack, because the moment you undo
an assignment the literal simply becomes non-false again and stays a
valid watch. This project's `dfs` search, since Stage 5, instead uses
an **explicit stack of independent, fully cloned assignment
snapshots** — there is no shared trail and no backtrack-undo step at
all; every branch just gets its own copy of everything it needs.

Rather than rebuild `dfs` around a persistent trail (a much bigger
change than "an internal data-structure upgrade," and not what
`STAGE9.md` asked for), each stack entry (`searchNode`/`SearchNode`)
now carries **both** the assignment **and** its own 2-literal-per-clause
watch array, and both are cloned together whenever a branch forks
(`cloneWatchState` in Go; `WatchState`'s `derive(Clone)` in Rust). The
static, never-mutated per-variable occurrence lists from Stage 2 are
still used, but only as a cheap way to enumerate *candidate* clauses
when a literal becomes false — each candidate then gets an O(1) check
("is this clause actually watching that literal right now?") before
paying for anything deeper. Most candidates fail that check and are
skipped instantly; only the ones currently watching the falsified
literal get the real work (shed the watch onto another literal, or
force/reject). This is exactly what the classic scheme buys you — most
clauses are never touched — just built on top of "clone what you need
per branch" instead of "share one trail and undo."

The alternative I considered and rejected was a separate mutable
reverse-watcher index (`literal -> clauses watching it`) instead of
reusing the occurrence lists as a filter. That would let `bcp` skip
straight to only-the-watching-clauses with no `continue`-and-check
step at all, but it would itself need to be cloned (or otherwise kept
consistent) per branch, which is exactly the cost this design avoids
by using the occurrence lists (already static and shareable) as the
candidate source and pushing the "is this actually being watched"
check down into an O(1) comparison instead.

### The bootstrap unit-propagation requirement

Watched literals require every clause to have at least two literals —
a unit clause has no second literal to shed a watch onto. Stage 8's
preprocessing pipeline already unit-propagates to a fixpoint by
default, so this is usually already true by the time `dfs.Run`/`run`
is called. But `--no-preprocessing`/`-x` bypasses that, so `Run`/`run`
now does its own defensive, one-time bootstrap unit-propagation pass
directly on a copy of the clauses, before ever building watch state —
reusing `preprocess.UnitPropagate`/`preprocess::unit_propagate`, newly
exported from the Stage 8 package for exactly this purpose. This is a
no-op in the common case (preprocessing already ran) and correct
either way. If the bootstrap pass or the initial watch-state
construction itself discovers a contradiction, `Run`/`run` reports
`UNSAT` immediately without ever pushing a search node.

One consequence worth flagging: `SelectVar`/`select_var` and
`SelectVarFastPick`/`select_var_fast_pick` need to operate on the
*bootstrapped* clause set (which can differ from `problem.Clauses` when
`-x` is combined with a raw file that has actual unit clauses in it),
not the original. Both implementations now wrap the bootstrapped
clauses in a local `workingProblem`/`working_problem` value and pass
that to the heuristics instead of the original `problem` — no other
change to either heuristic was needed, since bootstrap-fixed variables
are already set in the root assignment and both heuristics already
skip already-assigned variables, so those values simply carry forward
unchanged into every descendant branch (including whichever one is
eventually returned).

## Verification

- **Unit tests** (both languages, mirrored): the watch-primitive
  functions (`chooseWatch`/`choose_watch`, `isFalse`/`is_false`,
  `newWatchState`/`new_watch_state`, `cloneWatchState`/`WatchState`'s
  `Clone`) in isolation, plus `BCP`/`bcp` itself — including a
  contradiction test redesigned from scratch, since the old test's
  clause set included a length-1 clause that's invalid input for the
  new precondition. The replacement drives a genuine contradiction
  purely through watch movement across four length-≥2 clauses, with
  no pre-existing unit clause. `go test ./...` (8 packages, all
  passing) and `cargo test` (112 tests, all passing).
- **Node-count equivalence**: a throwaway harness (not committed, per
  the project's cleanup convention) called `dfs.Run` directly and
  confirmed **identical `NumNodes`** between the pre-Stage-9 and
  post-Stage-9 Go binaries across 150 real UNSAT benchmark files —
  exactly as expected, since `STAGE9.md` describes this as a
  data-structure upgrade to `BCP`, not a change to the search
  algorithm itself. This is the strongest available confirmation that
  the port preserves search behavior exactly, not just final verdicts.
- **Independent solution verification**: a from-scratch Python script
  (parses the DIMACS file itself, never reusing solver code) checked
  every returned satisfying assignment against every original clause,
  across both `SelectVar` variants and both preprocessing states (4
  flag combinations x 30 files x 2 binaries = 240 checks): **0
  violations**, for both the Go and Rust binaries.
- **SAT/UNSAT ground-truth cross-check**: ran the actual built CLI
  binaries (not library calls) against SATLIB's `uf100-430`/
  `uuf100-430` sets, whose filenames encode the known verdict: **100/100
  correct for Go, 80/80 correct for Rust**.
- **Go-vs-Rust parity**: direct comparison of verdicts across 60 files
  x 4 flag combinations (240 runs): **0 mismatches**.

## Performance

Per `STAGE9.md`'s framing, the point of this stage is to make BCP
itself cheaper, not to change the search tree. To isolate that, I built
a pre-Stage-9 baseline binary from each language's Stage 8 source (via
`git stash`, building, then restoring) and timed both versions against
150 real UNSAT 100-variable benchmark files (`-p 1 -x`, so BCP cost
dominates over the variable-selection heuristic and preprocessing
doesn't remove the very clause-shape this is meant to speed up):

| | old BCP (occurrence-list rescan) | new BCP (watched literals) |
|---|---|---|
| Go | 23.4s | 20.4s (~13% faster) |
| Rust | 13.7s | 9.9s (~28% faster) |

Both languages show a real, consistent speedup at identical node
counts (confirmed separately above) — i.e. this is genuinely cheaper
per-node work, not a smaller search tree. The gap between the two
languages' improvement is unsurprising: this benchmark set's clauses
are short (3 literals), so watched literals mostly save "look at every
occurrence of a falsified literal" in favor of "look at two watches
plus, when a watch is hit, one linear scan of that one clause" — a
win that compounds more visibly in Rust's tighter, allocation-light
loop than in Go's, but shows up in both.

I'd expect this speedup to grow much more pronounced on formulas with
longer clauses or once CDCL is added on top (which calls BCP far more
often, once per conflict, per `STAGE9.md`'s own note) — this
benchmark set's uniform random 3-SAT clauses are short enough that the
old occurrence-list rescan was never *that* expensive per clause to
begin with.

## Command line arguments

None. Per `STAGE9.md`: "I don't see any reason to make this optional
... the watched literal version will be the only version of BCP." No
new flag was added in either language.

## Testing

- Go: `go build ./...`, `gofmt -l .`, `go vet ./...`, and
  `go test ./...` (8 packages) are all clean.
- Rust: `cargo fmt`, `cargo clippy --all-targets -- -D warnings`, and
  `cargo test` (112 tests) are all clean.
- Manual/scripted verification as described above.

## Open questions / notes for you

- **Design judgment call**: cloning a per-branch `WatchState` alongside
  the assignment, rather than restructuring `dfs` around a single
  persistent backtracked trail, was a deliberate choice to keep
  `STAGE9.md`'s "internal data-structure upgrade, not a new algorithm"
  framing literally true and to avoid touching `dfs`'s overall search
  structure from Stage 5. The tradeoff: this clones an extra `O(clauses)`
  array per branch on top of the assignment clone `dfs` already did,
  which a trail-based design wouldn't need. If Stage 10+ moves to CDCL
  (which typically *wants* a persistent trail anyway, for clause
  learning and non-chronological backtracking), this may be worth
  revisiting then rather than now — flagging it so it's a conscious
  choice rather than a surprise later.
- No language/toolchain version changes needed.
- No new CLI surface, as instructed.
