# Report: Stage 20

## What this stage is

Per `STAGE20.md`, no code changes this stage: it's a design discussion
about multithreading `--algorithm=cdcl`, working through your
follow-ups to `REPORT16.md`'s CDCL section and a choice between four
concrete architectures. I built and validated one prototype in `util/`
to answer the pointed "can you implement [continuous clause sharing]
with minimal contention?" question with evidence rather than opinion,
the same way `util/termination/` validated Stage 18's design before
any real code was written. Nothing under `go_src`/`rust_src` was
touched.

**Bottom line up front**: I recommend **Option B** (a shared,
continuously-updated clause pool; no domain splitting; no restart
synchronization across threads) over Option A, Option C, and my own
prior "Option D" recommendation from `REPORT16.md`. The prototype
below shows continuous sharing at minimal contention is genuinely
achievable, which removes the main reason I'd previously leaned toward
batched, restart-boundary-only sharing -- so my own earlier
recommendation is superseded by what follows, not just restated.

## Shared clause database: can it be done with minimal contention?

**Yes.** I built and stress-tested a prototype
(`util/clausesharing/go/` and `util/clausesharing/rust/`) of the
mechanism essentially every non-portfolio parallel CDCL solver in the
literature actually uses for this: each thread owns a small, fixed-
capacity ring buffer ("export buffer") that it alone publishes newly
learned clauses into (filtered to short/low-LBD ones, as ManySAT does
with a length-8 cutoff), and every other thread drains other threads'
buffers independently, at whatever cadence it likes, with **no lock
anywhere in the hot path on either side.** ManySAT's own description
of this is "communication between the solvers of the portfolio is
organized through lockless queues which contain the lemmas that a
particular core wants to exchange," and the Painless framework (a
widely-used, actively maintained parallel-SAT-solver toolkit) wraps a
lock-free MPMC queue for exactly this, discarding clauses a slow
consumer doesn't keep up with rather than ever blocking a producer
([ManySAT](https://www.researchgate.net/publication/220163323_ManySAT_a_parallel_SAT_solver);
[PaInleSS](https://www.lrde.epita.fr/dload/papers/le-frioux.17.sat.pdf)).

**Why lock-free is even possible here, structurally**: a shared clause
pool has a property Stage 18's termination detection did not. Losing a
share is never a correctness problem -- if a reader misses a clause
because the writer overwrote its slot first, the only cost is a missed
optimization (some thread might re-derive it later, at real but
bounded cost); nothing about SAT/UNSAT correctness depends on which
shared clauses arrive, or when. That's exactly what licenses a lossy,
non-blocking design where a slow reader can simply be lapped by the
writer, rather than needing backpressure or locking the way
termination detection's exactly-once counter did.

**One real design mistake worth recording, since it directly shapes
what I'd actually recommend building.** My first version of the
prototype used a classic seqlock: a sequence counter flanking an
in-place write, with readers discarding "torn" reads caught mid-
update -- the standard C/kernel technique for exactly this shape of
problem. `go test -race` correctly rejected it: the torn-read case is
two goroutines touching the same plain memory with no happens-before
edge between them for that specific interleaving, and Go's race
detector (like Rust's aliasing rules) has no way to special-case "this
particular race is fine, trust me," even though the seqlock protocol
does safely discard that exact read afterward. The fix, which the
final version of the prototype uses: never touch a slot's payload in
place. Each slot is instead an atomic pointer to a freshly allocated,
*immutable* published clause (`atomic.Pointer[T]` in Go;
`arc_swap::ArcSwapOption` in Rust, which solves Rust's harder version
of the same problem -- with no GC, an atomic pointer swap needs its
own answer for "when is it safe to free the value a swap just
replaced," which `arc-swap`'s `Arc`-refcounting gives for free, the
same way this project reached for `crossbeam-deque` in Stage 18 rather
than hand-rolling a Chase-Lev deque in Rust). One atomic operation on
each side, no plain-memory interleaving ever occurs, both languages'
tooling are satisfied by construction rather than by argument.

**Validation results**: two stress tests per language --  one writer
thread hammering a single ring buffer against 16 concurrent readers,
and a 32-worker N-to-N topology where every worker publishes to its
own buffer while draining all 31 others' -- run for 300ms bursts,
checking every single clause any reader ever observed against a
deterministic "what should this ID's content actually be" oracle.

| | Go (`-race`, 5x repeat) | Rust (release, 4x repeat) |
|---|---|---|
| Total reads observed | ~1.0-1.2M per run (race build; slower) / 12-15M (normal build) | 5-15M per run |
| Corrupted reads | **0** | **0** |
| Data races reported | **0** | N/A (no runtime detector; no `unsafe`) |

Zero corruption, zero races, across tens of millions of reads in both
languages. I'm confident this design is sound and buildable for real
inside `internal/cdcl` when we get there. As with `util/termination/`,
this prototype is not imported by `go_src`/`rust_src` and is left in
the repo for you to commit if you want it kept.

## Work-stealing

Understood -- no further design needed here; `REPORT16.md`'s
conclusion stands (a CDCL trail's `reason[v]` pointers into a specific
thread's clause database make handing off a *live* decision stack
between threads unsafe, unlike DFS's fully self-contained
`WatchState`). This constraint is what rules out Option C's implicit
premise (that BFS-seeded regions can be redistributed the way `dfs`'s
deque redistributes stolen branches) and is a big part of why I'm
recommending Option B below, which never needs to redistribute
anything live at all -- only immutable learned clauses cross a thread
boundary, ever.

## Simplifying rules

Understood -- subsumption yes, BVE no, as agreed. This still applies
under Option B: the shared pool being continuously updated rather than
periodically merged doesn't change the argument (BVE removing a
variable from a shared pool while every thread's own live trail still
has that variable in play would be unsound regardless of how often the
pool is touched).

## Database memory limit

Your resolution -- per-thread, so a shared design costs
`num_threads * memory_limit` in aggregate -- is the right call, and
Option B actually makes the "combine only short/hot clauses so nobody
exceeds their limit" mechanism you described unnecessary, rather than
just simple to satisfy. Under continuous per-clause sharing, there is
no periodic "merge everyone's full database, filter to short clauses,
broadcast the combined result back out" step at all: each thread
imports individual already-short, already-filtered clauses one at a
time, straight into its own existing local database, subject to that
database's own existing memory limit and Stage 12's
`reduceClauseDatabase`/`reduce_clause_database` eviction exactly as if
the thread had learned the clause itself. A thread that imports more
than it has room for simply triggers its own existing reduction logic
sooner -- no new memory-accounting mechanism needs to be built. The
export ring buffers themselves are a separate, tiny, fixed-size
structure (tens of KB per thread at a realistic capacity) that sits
outside the `--alg-params` limit entirely, the same way, say, the
watch-list bookkeeping isn't counted against it today.

## Options

### Evaluating A, C, and D against what the prototype shows

**Option A** (fully independent per-thread databases; BFS-seed and
restart in lockstep; combine+simplify+redistribute only at restart
boundaries) is the one design here that doesn't benefit at all from
the prototype above -- it was built around the assumption that
continuous sharing wasn't worth the implementation cost, which the
prototype now shows isn't true. It's still the easiest to implement
and would still work, but I'd be deliberately giving up the freshness
benefit clause sharing exists for, for a simplicity reason that no
longer holds. I wouldn't pick this now.

**Option D** (my own `REPORT16.md` recommendation: restart-boundary
sharing, redistribute only at the same granularity) has the same
issue -- it's the design I picked specifically *because* I assumed
continuous sharing needed more machinery than restart-boundary
batching, and hadn't actually tried building the continuous version
yet. The prototype changes that assumption, so I'm revising my own
prior recommendation rather than defending it.

**Option C** (shared pool, continuous or not, plus BFS-seeded starting
regions redistributed on an adaptive "N nodes processed AND some
percentage idle" restart trigger) is the most sophisticated of the
four, and I don't think it's the right first build. Three reasons:
first, it reintroduces exactly the "who gets which unclaimed region
when someone's ahead" load-balancing problem Stage 18/19 already spent
real effort on for `dfs`, except harder, because CDCL can't hand off a
*live* region the way `dfs`'s deque hands off a live branch -- only a
fresh, never-touched one, so an imbalanced tree leaves fast threads
genuinely idle (not stealing) until the next adaptive restart fires.
Second, the percentage-idle threshold is a real free parameter with no
principled starting value (you said as much yourself -- "no idea,
make it a parameter to optimize later"), which means a real tuning
effort before it can be evaluated fairly. Third, and most
fundamentally: domain splitting under CDCL doesn't obviously pay for
itself the way it does under `dfs`. `dfs` has no clause learning at
all, so two threads exploring overlapping regions do genuinely
redundant work with nothing to show for it -- that's exactly why
splitting the tree helped so much in Stages 18/19's UNSAT benchmark.
CDCL's whole point is that clause learning already prunes redundant
work *within* a single search; two CDCL threads with diversified
phase/branching heuristics exploring "the same" nominal region usually
aren't doing the same work twice, because they're not making the same
decisions, and whatever one learns propagates to the other for free
under continuous sharing regardless of whether their regions overlap.
I wouldn't rule Option C out forever (see "worth trying later" below),
but I don't think it's the first thing to build.

### Recommendation: Option B, informed by the prototype

**Option B** -- one continuously shared clause pool, each thread
running the full, ordinary single-threaded CDCL loop over the
complete original problem with no BFS seeding and no restart
synchronization, diversified via randomized top-level phase/branching
choices, first satisfying-or-exhausted result wins via the same CAS-
based single-winner shutdown this project already has proven out
twice (Stage 17's `hc`/`ws`, Stage 18's `dfs`) -- is what I'd actually
build first. The chess-engine analogy you raised is apt and worth
naming directly: this is structurally the same idea as "Lazy SMP,"
the threading model modern chess engines (Stockfish among them) have
converged on -- every thread searches the *same* position with the
*same* code, sharing only a transposition table, relying on natural
timing/scheduling variance between threads (not explicit domain
splitting) for diversity, because in practice that turns out to
out-perform explicitly partitioning the search tree. The SAT
literature backs the same conclusion for CDCL specifically: ManySAT,
Plingeling, and Glucose-Syrup are all portfolio-plus-clause-sharing
designs, not divide-and-conquer, and divide-and-conquer/"cube-and-
conquer" approaches in the SAT world are mostly reserved for
specialized, very-large or provably-hard-UNSAT problems rather than
used as a general-purpose default.

Why I like it over the alternatives, concretely:

- **No new synchronization hazard at all.** Nothing is ever
  redistributed except immutable, already-filtered clauses, so it
  sidesteps the reason-pointer problem completely (nothing being
  shared can ever be `reason[v]`-referenced by anything). "I was
  afraid of this" about work-stealing turns out not to matter here --
  Option B never attempts it.
- **No restart synchronization across threads.** Each thread restarts
  on its own schedule (any Stage 15 restart strategy, independently
  chosen or even varied per thread for diversification, without
  needing to agree with anyone else about timing) -- one whole class
  of coordination bug (the kind Options A/C both need: "did everyone
  actually stop for the redistribution point yet") simply doesn't
  exist in this design.
- **Reuses proven machinery.** The CAS-based single-winner shutdown is
  the exact mechanism from Stage 17/18, already tested at 128 threads
  under `-race`. Per-thread RNG derivation is the exact pattern from
  Stage 17/18 too, just now also driving each thread's diversified
  phase-selection bias, not only tie-breaking.
- **Memory limit story is the cleanest of the four options**, as
  covered above -- no merge step to bound, ever.

What I'd give up, honestly: on an instance where the search space
really does have large, cleanly separable, non-overlapping hard
regions, true domain splitting (Option C) could in principle out-
perform pure portfolio-with-sharing, especially on very large
UNSAT-heavy instances -- the literature doesn't claim portfolio always
wins, just that it's the more common and more robust default. Given
"we can use git branches to try more than one," my suggestion: build
Option B first (it's the lower-risk, better-evidenced choice, and
gives every future stage a working multithreaded CDCL to compare
against), and treat Option C as a real candidate for a later branch
specifically if a benchmark turns up an instance class where B
plateaus and a domain-split design looks like it would help --
diagnosed from real numbers, not built speculatively.

## Benchmark: do you need additional or different SAT problems?

**Yes, for one specific reason, though not urgently.** I looked into
what the clause-sharing literature actually says about which instance
types benefit from sharing, since it directly bears on whether this
project's existing benchmark set (all uniform random 3-SAT) can show
Option B actually working: **"for random 3-SAT problems, clause
sharing is essential for unsatisfiable instances while not
significant and even slightly detrimental for satisfiable problems. In
contrast, for application benchmarks, both diversification and clause
sharing are highly beneficial for satisfiable as well as unsatisfiable
instances"**
([Community and LBD-Based Clause Sharing Policy for Parallel SAT Solving](https://pmc.ncbi.nlm.nih.gov/articles/PMC7326468/)).
The reason given is structural: industrial/application instances have
real modular structure (variables genuinely more tightly linked to
some other variables than others) that random 3-SAT, by construction,
doesn't have -- so a clause one thread learns is much more likely to
be useful to a different thread's different search region on a
structured instance than on an unstructured random one.

Practically, this means: **the existing benchmark set (including
Stage 19's new `uf175-753`/`uuf175-753`) is fine for verifying Option
B's basic correctness and for seeing the UNSAT-side clause-sharing
benefit** -- exactly the case the quote above says sharing helps with,
and exactly the case Stage 18/19 already built good infrastructure to
measure (a hard-but-tractable UNSAT instance to time end-to-end). But
**to actually see the SAT-side benefit this design is also supposed to
provide** (and to see whether Option C's domain-splitting idea would
ever pay for itself, per the discussion above), **I'd want a handful
of structured/industrial instances**, not more random 3-SAT at a
different size. A quick, low-effort way to get some: SATLIB
(`https://www.cs.ubc.ca/~hoos/SATLIB/benchm.html`, the same source
this project's existing `benchmark/` set came from) also hosts a
non-random "DIMACS" collection -- circuit fault-diagnosis (`ssa*`,
`c*`), graph-coloring (`flat*`, `g*`), and blocks-world planning
(`bw_large*`) instances among others. A dozen or two of these, mixed
SAT/UNSAT, at a size that's hard-but-tractable single-threaded
(similar to how Stage 19 picked `uf175`/`uuf175`'s difficulty), would
be enough to meaningfully evaluate the SAT-side clause-sharing claim
once Option B actually exists. Not urgent for this stage -- there's no
code to test yet -- but worth having ready before the implementation
stage's own benchmark section needs it.

## Testing

No code changed under `go_src`/`rust_src`. The new
`util/clausesharing/` prototype: `go build ./...`, `go vet ./...`,
`gofmt -l .` clean; `go test -race -count=5 ./...` clean (0 data
races, 0 corrupted reads across ~5.5M total reads under the race
build); `cargo fmt --check`, `cargo clippy --all-targets -- -D
warnings` clean; `cargo test --release` clean across 4 repeats (0
corrupted reads across ~35M total reads).

## Open questions / notes for you

- **My `REPORT16.md` "recommended resolution" (Option D) is
  superseded by this report's recommendation**, not merely restated --
  I'm flagging that explicitly since you referenced it by name in
  `STAGE20.md`. The earlier recommendation assumed continuous sharing
  wasn't worth building; this stage's prototype shows it is, which
  changes the answer.
- **Which restart strategy would each Option-B thread use?** I'd
  default to letting each thread pick independently (e.g. all default
  to `RestartPolynomial` per Stage 15, or -- more in the spirit of
  diversification -- vary the strategy or its constants per thread the
  way ManySAT varies solver configurations across its portfolio). Not
  resolved here; worth deciding when this actually gets built.
- **Structured/industrial benchmark instances**: per the Benchmark
  section above, I'd like a small set if/when you're able, but it's
  not blocking anything right now.
- `util/clausesharing/` is left in the repo per the same "anything
  left in `util/` is assumed wanted" convention as
  `util/termination/`; I have not committed it myself.
- `SeqCst` used throughout the new prototype's atomics, consistent
  with the project's standing directive.

Sources:
- [ManySAT: a parallel SAT solver](https://www.researchgate.net/publication/220163323_ManySAT_a_parallel_SAT_solver)
- [PaInleSS: a Framework for Parallel SAT Solving](https://www.lrde.epita.fr/dload/papers/le-frioux.17.sat.pdf)
- [Community and LBD-Based Clause Sharing Policy for Parallel SAT Solving](https://pmc.ncbi.nlm.nih.gov/articles/PMC7326468/)
