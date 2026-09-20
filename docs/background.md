# Background

A tour of the ideas behind `vibe_sat`, roughly in the order they were
built (see `prompts/STAGEx.md` and `reports/REPORTx.md` for the
blow-by-blow — this page is the "why," those are the "what happened").
[references.md](references.md) has the papers cited below.

## DFS / DPLL

`--algorithm=dfs` is a depth-first search over partial variable
assignments, in the classic DPLL (Davis–Putnam–Logemann–Loveland)
style: at each node, pick an unassigned variable, try setting it to
`false`; if that leads to a contradiction, try `true`; if *that* also
contradicts, backtrack. The engine that makes this practical is
**boolean constraint propagation (BCP)**: whenever a clause has only
one unassigned literal left, that literal is *forced* true (otherwise
the clause would be violated) — one forced assignment often cascades
into many more, pruning huge parts of the search tree for free before
any real branching happens.

`dfs` is a **complete** algorithm: if it exhausts the search space
without finding a satisfying assignment, that's a proof the formula is
unsatisfiable, not just "didn't get lucky." That's the fundamental
difference from `hc`/`ws` below.

Two variable-selection heuristics are available (`--alg-params`
`val1`): a weighted one that scores every not-yet-satisfied clause on
every node (more expensive, smaller tree) and a cheap static-order one
that just picks the lowest-numbered unassigned variable (faster per
node, bigger tree). Measured directly on this project's own benchmark
set, the cheap heuristic is 7-8x faster per node but visits 9-73x more
nodes — a real trade-off, not a strict improvement either way.

BCP itself is implemented with **watched literals** (Moskewicz et
al.'s Chaff): rather than rescanning every clause a variable appears in
whenever it's assigned, each clause only "watches" two of its literals
at a time, and is only re-examined when one of those two becomes
false. This turns BCP from "touch every occurrence" into "touch only
the clauses that could plausibly have changed status" — the single
biggest reason `dfs` and `cdcl` can handle formulas with thousands of
clauses at all.

## CDCL

`--algorithm=cdcl` starts from the same DPLL skeleton as `dfs` but adds
the single biggest idea in the history of SAT solving: **when
propagation hits a contradiction, don't just backtrack one level and
try the other branch — figure out *why* it happened, and use that.**

Concretely: every forced assignment records *which* clause forced it
(its "reason"). When BCP finds a clause fully falsified, `cdcl` walks
backward through the chain of reasons that led there, resolving them
together until exactly one literal from the *current* decision level
remains (the "first unique implication point," or first UIP). That
resolution process produces a brand new clause — a fact about the
formula that wasn't explicitly written down before, but is logically
implied by it — which gets added to the clause database permanently.
This is **clause learning**.

The learned clause also tells you exactly how far back you need to
jump: not necessarily just one level, but however far back the clause
itself only becomes a *unit* clause (all its literals false except
one) — which might skip over several decision levels of now-irrelevant
choices at once. That's **non-chronological backtracking**, described
in its own section below.

`cdcl` is `dfs` with a persistent trail (a single, incrementally
backtracked assignment history) instead of `dfs`'s independent
per-branch snapshots — a persistent trail is what makes it possible to
walk backward through *reasons* the way conflict analysis needs to.
Plain, unmodified conflict-driven learning turned out to actually
underperform `dfs` on this project's own hard instances until modern
branching heuristics (VSIDS/LRB) and restarts were added on top —
"bare" CDCL is a known weak spot in the literature on structureless
random instances specifically, and this project reproduced that
finding directly before fixing it.

## WalkSAT

`--algorithm=ws` is entirely different in spirit from `dfs`/`cdcl`: no
backtracking, no partial assignments, no proof of unsatisfiability —
just a complete random assignment that gets locally improved, one flip
at a time, forever (or until it satisfies everything). Plain
hill-climbing (`--algorithm=hc`) only ever accepts a flip that strictly
improves the score, which means it gets permanently stuck the moment
every single flip would make things worse (a "local optimum") even
though the formula might still be very solvable from a different
starting point.

WalkSAT's fix: pick a currently *unsatisfied* clause at random, and
flip one of its variables — with some probability ("noise"), a
uniformly random one of the clause's variables; otherwise, whichever
variable breaks the fewest other currently-satisfied clauses. Crucially,
WalkSAT **always commits the flip**, even if the overall score goes
down. That's what lets it walk out of local optima that trap plain
hill-climbing — and measured directly on this project's own benchmark
set, the difference is not subtle: on one 100-variable sample, plain
hill-climbing solved 3 of 30 instances within a one-second budget;
WalkSAT solved all 30.

Like `hc`, `ws` is **incomplete** — it can report `SAT` (with an actual
witness) or `UNKNOWN` (ran out of time/tries), but it can never prove
`UNSAT`, because it never actually reasons about *why* a formula might
have no solution.

## Preprocessing

Before any algorithm sees the formula, `vibe_sat` runs a fixpoint loop
of four simplification techniques (disable with `--no-preprocessing`):

1. **Unit propagation** — the same idea as BCP above, applied once up
   front to whatever unit clauses the original formula already
   contains.
2. **Pure literal elimination** — a variable that only ever appears
   with one polarity across the whole formula can be fixed to whatever
   value satisfies all of its clauses, for free.
3. **Subsumption elimination** — if clause A's literals are a subset
   of clause B's, B is redundant (A already enforces everything B
   does) and can be dropped.
4. **Bounded variable elimination (BVE)**, specifically the NiVER
   rule: a variable can be eliminated entirely by resolving every
   clause that contains it positively against every clause that
   contains it negatively — but *only* if doing so doesn't increase
   the total clause count.

Eliminated/fixed variables are renumbered out of the problem entirely
(so later algorithms never waste effort on them), with enough
bookkeeping kept to reconstruct a full solution back in the *original*
numbering afterward.

**How much this actually helps varies enormously by instance shape.**
On uniform random 3-SAT (SATLIB's `uf*`/`uuf*` sets), these techniques
find almost nothing to do — random 3-SAT near the satisfiability phase
transition is specifically constructed to have very little exploitable
structure. On *structured* instances, the effect can be dramatic: one
blocks-world planning instance (`bw_large.c.cnf`, 3016 variables,
50457 clauses) spends the overwhelming majority of its *entire*
runtime in preprocessing alone, shrinking the formula meaningfully
before search even begins. Profiling that same instance found
subsumption elimination — not bounded variable elimination, which had
been guessed as the likely bottleneck — responsible for the vast
majority of that time, simply because its cost grows with the
*square* of the clause count. Subsumption elimination now uses
multiple threads (`--num-threads`) for exactly this reason.

## Restart strategies

CDCL search can get unlucky: an early decision sends it deep into a
part of the search space that takes a very long time to fully explore
and reject, even though the *learned clauses* collected along the way
would have made a fresh start much faster. A **restart** throws away
the current decision stack and starts over from the top — keeping
every learned clause, which is what makes this a net win rather than
wasted work.

`vibe_sat` offers four restart schedules (`--alg-params` `val2`,
`cdcl` only): no restarts, the classic Luby sequence
(1, 1, 2, 1, 1, 2, 4, ...), a quadratic "polynomial" growth schedule,
and a true geometric schedule (constant ratio between successive
restart intervals — see [references.md](references.md) for a note on
why this project ended up with both a "polynomial" and a "geometric"
option, and how that happened). Measured on this project's own
250-variable benchmark set, the polynomial schedule proved every
sampled UNSAT instance within a fixed time budget, against 6/10 with
no restarts and just 3/10 with Luby using its own literature-standard
base interval — restarting *too* often, before the branching heuristic
has time to settle, turned out to actively hurt. That measurement, not
the literature's own default, is why `cdcl` defaults to the polynomial
schedule with one thread.

With more than one thread, the default instead becomes **round-robin**:
worker 0 gets the polynomial schedule, worker 1 the geometric one,
worker 2 Luby, worker 3 back to polynomial, and so on — deliberately
diversifying the portfolio (see "multi-threaded CDCL" below) rather
than racing several identical searches against each other.

A fifth schedule, **Glucose's own data-driven policy** (`val2=5`;
Audemard & Simon, IJCAI 2009 — see [references.md](references.md)),
restarts on a signal from the search itself rather than a fixed
conflict count: it tracks a short-term moving average of the last 50
learned clauses' "Literal Block Distance" (LBD — the number of
distinct decision levels among a clause's literals, also used by
clause-database management below) alongside the all-time average
since the search began, and restarts whenever the recent average
looks close to `K` times the global one. `K` was tuned by benchmark
sweep (`reports/REPORT35.md`) to 0.6, not Glucose's own published 0.8
— restarting less often than the literature default turned out to
matter on this project's own benchmark mix (roughly halving mean
solve time at an equal or better solved count), the same pattern
already found for VSIDS-over-LRB and polynomial-over-Luby above. Not
(yet) this project's default regardless; see `reports/REPORT34.md`/
`reports/REPORT35.md` for why.

## Clause-database management

Learned clauses accumulate without bound unless something prunes
them, so once an optional memory limit (`--alg-params val3`) is
exceeded, `cdcl` deletes roughly the least useful half of the
clauses that are safe to delete (not currently locked as some
variable's reason, and not part of the original problem). "Useful" is
now judged by LBD first, MiniSat-style activity decay only as a
tiebreak: a clause at or below an LBD of 2 ("glue," in Glucose's
terminology) is never deleted regardless of activity, and among the
rest, the least "compact" (highest-LBD) clauses go first. Before
`reports/REPORT34.md` this was pure activity decay, with no notion of
LBD anywhere in `cdcl`.

## Learned-clause minimization

Once `analyze` derives a learned clause via first-UIP resolution
(above), a literal in it can still be redundant: already implied by
the clause's other literals together with the implication graph, and
so safe to drop without weakening the clause at all. `cdcl` checks
every non-asserting literal for this recursively — a literal's own
reason clause might not obviously qualify, but if *its* dependencies
trace back to something already accounted for, it qualifies too — the
same self-subsumption minimization MiniSat has used since 2005
(Sörensson & Biere, "Minimizing Learned Clauses," SAT 2009, formalized
it). A shorter learned clause is strictly better on every axis
`cdcl` already tracks: cheaper to store and re-examine later, and it
can only lower (never raise) both the backtrack level and the LBD.

Measured on this project's own hard benchmark instance
(`benchmark/uuf250-1065/uuf250-01.cnf`, single-threaded, VSIDS):
minimization cut both the number of conflicts needed (111,453 →
95,494) and wall-clock time (35.4s → 18.0s) — a real, substantial win,
not just a "cheaper clauses, same search" effect. See
`reports/REPORT36.md` for the full numbers, including a broader
benchmark sweep where it raised solved-instance counts noticeably
within a fixed time budget.

The recursive check is bounded, not open-ended: it memoizes every
variable it visits within one minimization pass (so no reason clause
is ever re-scanned twice for the same learned clause) and enforces a
hard, `O(n log n)`-shaped work budget on the total number of
reason-clause literals examined — once exceeded, whatever's left of
the clause is kept as-is rather than risking unbounded work on a
pathological implication chain. And because minimization only ever
reads and writes one thread's own private search state (exactly like
`analyze` itself), it needs no special handling in multi-threaded
`cdcl` runs — no shared state to contend with, no pause for other
threads to wait out.

## Non-chronological backtracking

Plain DPLL (what `dfs` does) backtracks *chronologically*: hit a
contradiction, undo the single most recent decision, try its other
branch. If a contradiction actually only depends on a decision made
much earlier, that's enormously wasteful — you re-explore every
intervening decision's other branch too, even though none of them had
anything to do with the actual problem.

CDCL's learned clause tells you precisely how far back the
contradiction's *real* cause goes: the second-highest decision level
among the learned clause's own literals (the highest is always the
level where the conflict was just detected). Jumping straight there —
skipping over every decision level in between, however many that
is — is non-chronological backtracking. This project verified the
effect directly, by hand-constructing a small formula where two
clauses alone force a variable's value regardless of some other,
unrelated decision that happened to trigger the conflict, and
confirming CDCL's analysis derives that forced value *without ever
looking at* the triggering decision — the clearest possible
demonstration that the learned fact is more general than "the last
guess was wrong."

## Multi-threaded DFS, work stealing

`dfs` parallelizes as genuine divide-and-conquer, not a portfolio: the
search tree really is split up, with each thread exploring a disjoint
piece of it. Concretely: a brief breadth-first expansion of the tree
first collects up to `--num-threads` starting branches (deep enough to
give every thread a roughly similarly-sized independent chunk), then
each thread runs its own depth-first search from its seed, using a
**work-stealing deque** — the thread's own descent pushes and pops one
end of its deque (LIFO, keeping locality), while any other thread with
work to spare can steal from the *opposite* end (FIFO) whenever its own
deque runs dry. Stealing from the far end specifically hands the thief
the oldest, and typically largest, unexplored branch rather than a
sliver the owner just created.

Go's deque started out mutex-guarded (simple, correctness-first) and
was later rewritten lock-free, matching a Chase-Lev-style design (see
[references.md](references.md)) much closer to what Rust's own
`crossbeam-deque` crate already provided out of the box — see "Go
versus Rust" below for what that comparison actually found.

Knowing when a work-stealing search is genuinely *done* (as opposed to
"everyone's momentarily out of work but a steal might still happen")
needs real care: a thread must not be declared idle just because its
own deque emptied — it must also have swept every peer and found them
all empty too, and even then a late-arriving handoff from a peer could
race that observation. `vibe_sat` uses a centralized quiescence
counter (not a fully distributed protocol like Dijkstra-Scholten's,
since this runs as one process with shared memory, not physically
separate machines) with an explicit re-check step exactly to close
that race.

## Multi-threaded CDCL, shared clause database

CDCL's parallelization looks nothing like `dfs`'s, for a structural
reason: a CDCL trail isn't a self-contained value the way a `dfs`
branch is — every forced assignment points back to the specific
learned clause that forced it, and that clause might only exist in one
thread's own database. Handing a live, mid-search CDCL trail to
another thread the way `dfs` hands off a branch would leave it holding
assignments it can't actually justify.

So `cdcl` instead uses a **portfolio design**: every thread
independently runs the ordinary single-threaded search over the
*entire, complete* original problem (no domain splitting at all), with
just enough diversification (mainly via round-robin restart schedules)
that they don't all do exactly the same thing. The first thread to
reach *any* verdict — SAT or UNSAT — is already the answer for the
whole run; every other thread stops. This mirrors "Lazy SMP," the
threading model modern chess engines converged on: several identical
searches sharing only a little state, relying on natural
scheduling/timing variance for diversity, rather than explicitly
partitioning the problem.

The "little state" they share is **learned clauses**, continuously:
every newly learned clause short enough to be worth sharing (an
8-literal cutoff, following the ManySAT design) is published to a
small, fixed-size, per-thread ring buffer that every other thread
drains at its own pace — a lock-free design (no thread ever blocks
waiting for another) validated with a stress-tested prototype before
being wired into the real search, and confirmed under real,
sustained multi-threaded pressure to cost under 1% of total CPU time
even at 32 threads on a 10-core machine. Clause sharing measurably
helps on this project's benchmark set almost entirely on the UNSAT
side — proving unsatisfiability needs the whole search space
genuinely covered, so a clause one thread learns can save a lot of
duplicated work elsewhere; a satisfying assignment, needing only one
lucky path, benefits much less.

## Testing, at every stage

Every stage of this project followed roughly the same verification
pattern, escalating with how risky the change was:

- **Unit tests for essentially every function**, in both languages,
  covering the same cases (mirrored test suites) so a divergence
  between the two implementations would show up as a difference in
  which tests exist, not just which pass.
- **An independent, from-scratch verifier for every claimed `SAT`
  result** — a script (later, a permanent tool; see
  `util/benchcompare/`) that parses the original CNF file and the
  reported solution *without reusing any of `vibe_sat`'s own code*,
  and checks every clause directly. A bug shared between the solver's
  parser and a checker built from the same parser would never be
  caught; an independent one has a chance.
- **Go-versus-Rust cross-checks** on every real feature: same
  benchmark files, same flags, checking that both languages agree on
  every verdict (and, early on before multi-threading made it
  meaningless, that they produced byte-for-byte identical output given
  the same seed).
- **`go test -race`** as standard practice from the moment
  multi-threading entered the project, including deliberately
  adversarial stress tests targeting the trickiest possible
  interleavings (e.g. a lock-free deque resizing its buffer while
  several other threads are actively trying to steal from it,
  repeated hundreds of times).
- **Standalone prototypes for the trickiest concurrency primitives**,
  built and stress-tested in isolation under `util/` *before* being
  ported into the real solver — termination detection, the shared
  clause-sharing ring buffer, and the lock-free work-stealing deque
  were all validated this way first. One of those prototypes caught a
  genuine, subtle data race (see "Go versus Rust" below) before it
  ever reached real search code.
- **Real profiling before optimizing**, not guesswork: several
  "obvious" performance guesses in this project turned out to be
  wrong when actually measured (see below) — profiling first is what
  caught that.
- **A CI smoke test that actually runs the built binaries**
  (`reports/REPORT37.md`). Everything above — unit tests, `cargo
  test`/`go test`, even `util/benchcompare`'s own test suite — checks
  code paths, never the compiled CLI itself; none of it would catch an
  argument-parsing regression or a build that compiles cleanly but
  panics the moment real input reaches it. CI now also runs a small,
  curated set of `--algorithm`/`--num-threads`/`--no-preprocessing`/
  `--alg-params` combinations (every individual value covered at least
  once, not a full cross product) against a couple of tiny
  `benchmark/` files, through `util/benchcompare --fail-on-issues` —
  genuinely cheap (each combination finishes in single-digit
  milliseconds), but it closes a real gap none of the rest of this
  list does.

## Go versus Rust: what this project actually found

The whole point of building `vibe_sat` twice (see the top-level
[README](../README.md)) was to compare the two languages directly on
identical work. A few concrete things fell out of that comparison:

- **Rust was consistently faster on the core search algorithms**,
  typically by something like 1.3-2x on `dfs`/`cdcl`, though the exact
  gap moved around by instance and stage.
- **A large, specific exception was found and fixed.** Rust's
  preprocessor was measured as roughly 1.8x *slower* than Go's on a
  real instance — the wrong direction. Profiling (with `perf`) traced
  this to Rust's default hash function (`SipHash`, chosen for
  resistance to adversarial input, not speed) being disproportionately
  expensive for hashing the very short clauses this project's
  preprocessor works with; replacing three hash-set-based lookups with
  plain linear scans over short slices fixed nearly the entire gap in
  both languages (it turned out to help Go too, just less
  dramatically) and restored the usual ordering.
- **Go's hand-rolled, mutex-guarded work-stealing deque was rewritten
  lock-free**, closer to Rust's own `crossbeam-deque`. Building and
  stress-testing the replacement in isolation first (see "Testing"
  above) caught a real, subtle bug under `go test -race`: a very
  delayed thread could observe a stale index that, after enough
  intervening work, aliased a slot another thread was actively
  overwriting. The algorithm was still logically correct even then —
  a failed compare-and-swap always discards the bad read — but Go's
  race detector correctly flagged the underlying unsynchronized memory
  access anyway, and the fix (an atomic pointer per slot instead of a
  plain value) satisfied it without changing the algorithm's actual
  guarantees.
- **Rust's ownership model caught concurrency mistakes at compile
  time** that Go could only catch at runtime, via `-race`, and only on
  whichever code paths a given test run happened to exercise. Go's
  equivalent safety net — the race detector plus deliberately
  adversarial stress tests — closed most of that gap in practice, but
  needed to be built and run explicitly; Rust got the same guarantee
  for free from the compiler on every build.
- **Go's standard library sufficed for everything** the project asked
  of it (`PROMPT.md`'s "no external packages" constraint for Go), while
  Rust reached for a small, deliberately general-purpose set of crates
  along the way (`clap` for CLI parsing, `rand`, `crossbeam-deque`,
  `arc-swap`) — never anything SAT-specific, per the project's own
  rule, but a real, structural difference in how much "comes with the
  language" between the two ecosystems.

## Other things worth knowing

- **Every benchmark claim in this project's reports was checked
  against real measurement on this project's own benchmark set**, not
  just cited from the literature — and more than once, real
  measurement won out over what a cited paper's own headline result
  would have predicted (VSIDS over LRB; a quadratic restart schedule
  over the Luby sequence's own literature-standard base interval; a
  MiniSat-style "blocking literal" optimization that looked obviously
  correct on paper but measured as a 25-28% *regression* on this
  project's mostly-short-clause benchmark set, and was reverted rather
  than kept). Treat any specific number in this documentation as a
  snapshot from this project's own particular benchmark set and
  hardware, not a universal claim about the technique in general.
- **Preprocessing, restarts, phase saving, and clause-database
  reduction are all fully deterministic** given the same input and
  thread count — the only genuinely non-deterministic part of
  `vibe_sat`'s behavior is *which* thread happens to win a race to a
  result once `--num-threads` is more than 1 (the verdict itself is
  always deterministic; only wall-clock timing and which worker
  reports it are not).
