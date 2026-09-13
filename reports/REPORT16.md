# Report: Stage 16

## What this stage is

Per `STAGE16.md`, no code changes this stage: it's a set of design
questions about adding multithreading to `vibe_sat`, across four
areas (thread/core model, `hc`/`ws`, `dfs`, `cdcl`), plus an open
invitation for other concerns. I researched current practice (Rust's
async-vs-thread split, work-stealing deque design, parallel/portfolio
SAT solver literature, and termination detection for shared-memory
search) to ground the answers below, cited inline. Nothing here is
implemented; it's meant to surface the real design decisions and
hazards before Stage 17 (presumably) writes any code.

## Threads vs. cores

**Oversubscription is fine to allow in both languages, for the
values anyone would realistically pass.** The two languages get
there very differently, though, and one thing in the stage
description needs correcting before we build on it.

**Go**: no problem, exactly as you expect. Goroutines are M:N
green threads multiplexed onto `GOMAXPROCS` OS threads (defaulting to
the logical core count) regardless of how many goroutines exist, so
`--num-threads` doesn't need to touch `GOMAXPROCS` at all -- it's
purely "how many concurrent search workers to run," and the runtime
decides how to time-slice them onto actual cores. Goroutines start
with a tiny (~2KB) stack that grows as needed, so oversubscribing by
even a large factor is cheap. This part of your plan is solid as
stated.

**Rust: I'd steer away from tokio for this.** Tokio is an *async I/O*
runtime -- its entire value proposition is letting a small number of
real OS threads cooperatively multiplex a large number of tasks that
spend most of their time *waiting* (on a socket, a disk read, a
timer). Our search workers are the opposite: long-running, CPU-bound,
never-yielding compute loops. Tokio only gets to switch between tasks
at `.await` points; a CPU-bound task that never awaits will simply
hog whatever OS thread it landed on and starve every other task
scheduled there. Routing it through `spawn_blocking` avoids that
starvation, but at that point you're just using tokio's blocking pool
as a roundabout way to get what `std::thread::spawn` gives you
directly, with extra machinery in between and none of tokio's actual
benefits (nothing here ever awaits I/O). As one discussion of exactly
this question puts it: "if your work is CPU-bound and does not
yield, plain `tokio::spawn` is not the right tool. Use
`spawn_blocking`, Rayon, or a dedicated thread-based design instead"
([users.rust-lang.org](https://users.rust-lang.org/t/why-shouldnt-i-use-tokio-for-my-high-cpu-workloads/63398)).

My recommendation: plain `std::thread::spawn` -- one real OS thread
per search worker -- plus the `crossbeam-channel` crate for the
coordination messages (status/work-request/work-grant/stop). I call
out `crossbeam-channel` specifically rather than `std::sync::mpsc`
because it provides a `select!` macro that lets one thread watch
several channels at once (e.g. "incoming stolen work" *and* "global
stop signal" *and* "incoming steal request"), which is structurally
the closest thing Rust has to Go's native `select` over channels --
this is the concrete answer to your "are the models close enough"
question below. `rayon` is worth keeping in mind too, but it's a
better fit for `hc`/`ws`'s embarrassingly-parallel "N independent
restarts" shape than for `dfs`/`cdcl`'s custom stack-of-stacks
work-stealing protocol, where you want direct control over the
handoff logic rather than an automatic scheduler.

Because `std::thread` maps 1:1 to a real OS thread (default 2-8MB
stack, real create/teardown cost), there genuinely is a point where
Rust-side oversubscription becomes wasteful in a way Go's never is --
but that point is in the hundreds-to-thousands-of-threads range, far
past anything anyone would sensibly pass as `--num-threads` for a
CPU-bound solver on real hardware. I don't think this needs a
hard-enforced cap; it's consistent with how this project already
treats other `--alg-params` values (validated ranges, no additional
"sanity" ceiling beyond what's logically required) to just let the
user ask for what they ask for.

**Are the two languages' threading models close enough for similar
code and structure?** At the level that matters for this project --
N long-lived worker loops plus message-passing channels for
coordination -- yes. Go goroutines+channels and Rust
`std::thread`+`crossbeam-channel` map onto each other closely enough
that both implementations can share the same message types and
control-flow skeleton, even though the underlying scheduling
primitives differ (Go: cooperative/preemptive M:N over OS threads;
Rust: 1:1 real OS threads). The one structural difference worth
calling out up front: Rust's ownership/`Send`/`Sync` rules will force
an explicit, compiler-checked answer to "who owns this piece of
search state right now, and is it being moved or shared" at every
hand-off point, whereas Go's shared-memory-by-default model lets you
reach for a mutex-guarded (or entirely unguarded) shared struct
instead, with `go test -race`/`go build -race` as the only thing
that catches a mistake -- and only on whichever code paths your test
run happens to exercise, and only at runtime. I'd expect the two
implementations to converge on the same design regardless (it's the
only correct one), but Rust's compiler gives you that check for
free; Go needs the race detector run explicitly, and it should become
a standard part of this project's Go test routine from here on.

## Hillclimbing (`hc`/`ws`)

The decomposition you propose -- N threads, each doing
`ceil(num_starts/num_threads)` starts -- is about as
low-risk as parallelism gets: each `hc`/`ws` restart already builds
its own independent randomized assignment from scratch (see
`hillclimb.go`/`hillclimb.rs`'s per-try state), so there's no shared
mutable *search* state between threads at all. The only shared things
are (a) the "someone found it, everyone stop" signal and (b) whatever
result ultimately gets returned.

**Time limit: I'd give each thread the full `time_limit`, not
`time_limit/num_threads`.** Since the threads run concurrently, giving
each the whole budget keeps the overall wall-clock time at
`time_limit` (not more), while the total number of restarts actually
attempted within that same wall-clock window scales with
`num_threads` -- which is exactly the benefit you're adding threads
to get. Dividing by `num_threads` would hold the aggregate
thread-seconds of work roughly constant (matching what one thread
alone would have done in `time_limit`) but pointlessly shrink the
wall-clock time actually spent, which throws away the point of
parallelizing a time-limited search: you'd finish sooner, sure, but
you already told the program how long you're willing to wait, and
there's no reason to stop early with more attempts still affordable
in the time you budgeted. That's my opinion, as asked for, but I'd
frame it as an internal convention rather than something to expose on
the command line -- consistent with this project's minimalist CLI
philosophy so far.

**Stop signal**: I'd use the same "shared flag, checked periodically"
mechanism this codebase already has for time limits (`dfs`/`cdcl`'s
`timeCheckInterval`/`TIME_CHECK_INTERVAL`-gated clock check) --
literally the same idiom, just checking a different condition. Go:
an `atomic.Bool`. Rust: `Arc<AtomicBool>`. This keeps the two
implementations structurally identical and reuses a pattern that's
already proven out in this codebase, rather than introducing a new
one.

One subtlety worth deciding now rather than discovering later: if two
threads find satisfying assignments at nearly the same moment, you
need a definitive single winner, not just "a flag got set." I'd use a
compare-and-swap on the flag itself as the arbitration point --
whichever thread's `CompareAndSwap`/`compare_exchange` from
false→true actually succeeds is the one whose result gets returned;
every other thread's own "I found one too" is simply discarded. (Go:
`atomic.Bool.CompareAndSwap`; Rust: `AtomicBool::compare_exchange`.)
That avoids a second race between "the stop flag is set" and "the
winning result is actually available yet."

## Depth-first search

The overall shape -- BFS to seed `num_threads` independent DFS
stacks, then a "need work"/steal protocol thereafter -- is a sound,
standard divide-and-conquer parallel search design, and it happens to
fit *this* codebase's existing `dfs` particularly well: Stage 9's
per-branch `WatchState` is already fully cloned and self-contained at
every branch (no shared mutable trail the way `cdcl` has), so every
seed or stolen state is already a complete, independent,
handoff-safe unit with zero extra bookkeeping required. That's a
genuinely nice side effect of the Stage 9 design choice, and it's the
main reason `dfs` parallelizes far more easily than `cdcl` will (see
below).

That said, I see a few concrete issues with the outline as written:

**1. Steal from the opposite end the owner works from, not the same
end.** The outline has "any other thread with a stack of size 2 or
more can pop off its *top* partial assignment" -- stealing from the
same end the owner itself pushes/pops from tends to hand the thief
the smallest, freshest, least-explored-of-the-remaining branch, which
for a LIFO stack often means a near-trivial sliver of work, so the
thief is back asking for more almost immediately, generating a lot of
request traffic for little benefit. The standard fix, used by
essentially every mature work-stealing scheduler (the Chase-Lev
deque, which underlies Rust's own Rayon internally) is asymmetric:
the owner works LIFO from one end (push/pop the *bottom*), while
thieves steal FIFO from the *other* end (steal the *top*) -- "push
inserts a task at the bottom... steal tries to remove a task from the
top... local worker LIFO consumption and remote FIFO stealing"
([Chase-Lev deque discussion](https://arxiv.org/pdf/2309.03642)). This
tends to hand stolen work the oldest, least-explored, and typically
largest branches instead. I'd recommend each thread's local stack be
a genuine double-ended structure, with steal-requests always serviced
from the opposite end from local push/pop.

**2. Termination detection needs care, but doesn't need a fully
distributed protocol.** The classic algorithms here (Dijkstra-Scholten,
token-ring) are designed for systems with no shared memory and no
"just ask a coordinator" option
([Dijkstra-Scholten algorithm](https://en.wikipedia.org/wiki/Dijkstra%E2%80%93Scholten_algorithm)).
We have neither of those constraints -- this is one process, shared
memory, and a small number of threads even under real-world
oversubscription -- so a single centralized coordinator (a mutex- or
channel-protected idle counter) is simpler and entirely sufficient;
the termination-detection literature itself notes simple
shared-memory approaches are adequate at the scale ("no more than 16
workers") this project will realistically see
([taxonomy of termination detection algorithms](https://ranger.uta.edu/~weems/NOTES4351/TDtaxonomy.pdf)).
The subtlety to get right: a thread must not be counted "idle" merely
because its own stack emptied -- it must have *also* requested work
and gotten back a definitive "nothing anywhere" answer, or you get a
race where thread A reports idle exactly while thread B's steal
transfer to A is in flight, and a naive "idle count == N" check fires
a false UNSAT in the gap between B's send and A's receive. I'd route
every cross-thread work handoff through the same coordinator (or have
it witness every handoff), so it never counts a thread idle while a
transfer to that thread is outstanding. This is fiddly enough that
I'd want a small standalone prototype/spike of just the termination
protocol before wiring it into the real search, rather than finalizing
it purely on paper now.

**3. Fewer seeds than threads.** If the formula is small enough, BFS
seeding might exhaust the search (or resolve SAT/UNSAT outright)
before ever producing `num_threads` seeds. I don't think this needs
special-case code, actually: threads that never received a seed
simply report themselves idle immediately and wait, which the
termination protocol above already has to handle correctly regardless
(a thread can go idle at any time, not just "at the end"). The one
explicit early-exit worth adding: if BFS itself proves UNSAT (or hits
a satisfying assignment) while still generating seeds, report the
answer immediately and never spawn the worker threads at all.

**Can we do something similar in Rust? Yes, and I think Rust's
ownership model is a genuine advantage here, not just a parity
story.** Each local DFS stack should be owned by exactly one thread
at a time, never touched by two threads concurrently -- and Rust's
type system can make that a *compile-time* guarantee rather than a
convention: model a steal as literally *moving* an owned branch value
through a channel (`Sender<Branch>`/`Receiver<Branch>`); once thread A
sends a branch to B, the compiler won't let A touch it again, full
stop. There's no way to accidentally retain a reference to something
you "gave away," unlike Go, where nothing stops a goroutine from
keeping a pointer into a struct it sent over a channel except
discipline, review, and `go test -race` catching it later, at
runtime, if your tests happen to exercise that path. For the
coordination channels I'd use `crossbeam-channel`'s `select!` for the
reasons given above.

Also worth flagging: `crossbeam-deque` is a ready-made, battle-tested
Chase-Lev work-stealing deque (the same one Rayon uses internally).
Rather than hand-rolling the "stack + need-work channel + steal
servicing" protocol from scratch in Rust, `crossbeam_deque::Worker`/
`Stealer` could be used directly for each thread's local stack, with
only the termination-detection layer (which is search-specific, not a
generic scheduler concern) hand-rolled on top. Go has no equivalent
off-the-shelf library-level primitive to reach for (its own runtime
has one internally, for scheduling goroutines onto Ps, but it isn't
exposed for user code), so hand-rolling the deque in Go is both
necessary and idiomatic there. That's one place I'd expect the
*implementation strategy*, not the *algorithm*, to genuinely diverge
between languages: given this project's consistent practice so far of
keeping the two implementations structurally parallel even when a
fancier library option exists (Stage 12's reduction, Stage 13's
activity bookkeeping, Stage 15's restart schedules were all
hand-rolled in both languages rather than reaching for an available
crate/package), I'd lean toward hand-rolling the deque in Rust too for
parity -- but `crossbeam-deque` is there if less code and a
well-tested concurrency primitive are preferred once we're actually
implementing this.

## CDCL

I want to flag a real structural problem with layering the DFS
work-stealing protocol directly onto CDCL, and then a couple of
smaller points about the clause-sharing idea itself.

**The clause-sharing proposal (per-thread database, combined +
simplified + shared between restarts) is a reasonable starting
design, but it's worth knowing what it gives up.** In the parallel
SAT literature, essentially every non-portfolio ("divide and
conquer") parallel CDCL solver shares learned clauses *continuously*
-- as soon as they're learned, filtered by a cheap quality measure
like length (ManySAT shares clauses of length ≤ 8) or LBD/glue, not
batched at restart boundaries
([parallel clause sharing survey](https://drops.dagstuhl.de/storage/00lipics/lipics-vol305-sat2024/LIPIcs.SAT.2024.17/LIPIcs.SAT.2024.17.pdf);
[PaInleSS framework](https://www.lrde.epita.fr/dload/papers/le-frioux.17.sat.pdf)).
Under this project's Stage 15 restart schedules, restarts can be
hundreds to tens of thousands of conflicts apart (especially the
polynomial schedule's later restarts), so batching sharing only that
often throws away a lot of the benefit that makes clause sharing
worthwhile: a nearby thread might independently re-derive, at real
cost, a clause another thread learned long before. Continuous sharing
needs a low-contention shared queue (each thread has a local "outbox"
others drain), which is meaningfully more implementation work. I
think restart-boundary sharing, as you proposed, is a completely
reasonable and much simpler first cut -- possibly even a defensible
permanent choice, if the restart cadence stays fairly frequent -- but
it should be understood as a deliberate simplicity-for-freshness
tradeoff against what competition solvers do, not a free lunch.

**The bigger issue: work-stealing, done the DFS way, isn't safely
portable to CDCL, because of what a CDCL partial assignment actually
*is*.** A DFS partial assignment is a self-contained value array with
no external pointers. A CDCL trail is not: `reason[v]` for a forced
variable points *into a specific clause database* -- some variables on
a thread's stack may be there only because a learned clause (that
exists in *that thread's* local database, at an index meaningless
elsewhere, or that simply doesn't exist yet in another thread's
database) forced them via BCP. If thread A steals a live, mid-search
decision stack from thread B and just adopts it, A's own (different)
clause database can't justify several of those forced assignments --
which breaks `analyze`'s ability to walk back through the reason
chain if a future conflict touches that variable. This hazard doesn't
exist for DFS at all, and it's exactly the kind of thing the "load
balancing... is a challenge of the divide and conquer approach"
warning in the parallel-SAT literature is about.

My recommended resolution: **only redistribute work at the same
granularity as the BFS seeding step** -- steal an as-yet-unclaimed
top-level branch (a fresh decision, no conflict-derived reasons
attached), never a live mid-search decision stack. This dovetails
neatly with the clause-sharing design already on the table: the
natural moment to redistribute work is exactly when a thread restarts
anyway (it's back at level 0, with no foreign-reason baggage), and at
that same synchronization point it can (a) publish its own newly
learned clauses to the shared pool, (b) pull in the latest
merged/simplified pool, and (c) either resume its own seed region or,
if that region is now exhausted, claim a fresh unclaimed seed from
the shared pool the BFS step produced. That ties work redistribution
and clause sharing to the same synchronization point instead of two
independent mechanisms with a hazard between them, at the cost of
coarser load balancing than DFS gets (CDCL's smallest donatable unit
becomes "a whole top-level seed region," not an arbitrary mid-search
branch). Going further -- genuine mid-search work-stealing under
CDCL -- is close to a research problem in its own right; I wouldn't
recommend attempting it here.

**Simplifying the shared pool: subsumption yes, bounded variable
elimination no.** Subsumption elimination (already in this project's
Stage 8 preprocessor) is safe and useful to run over the union of
clauses learned across threads -- different threads solving different
regions will legitimately re-derive overlapping or dominated clauses,
and removing subsumed ones shrinks the pool for free. I would *not*
extend this to bounded variable elimination, though, even though it's
part of the same preprocessor: BVE removes a variable, and every
clause mentioning it, from the formula -- sound only if every
consumer agrees the variable is gone. Here, every thread is still
independently searching the *full* original variable set; eliminating
a variable from just the shared learned-clause pool, while every
thread's own local database and live trail still very much has that
variable in play, would produce an inconsistent view that doesn't
correspond to anything any thread is actually solving. So: run
subsumption elimination over the merged pool, but not BVE -- "something
like the preprocessor" needs to mean the safe subset of it, not the
whole thing.

**One more thing to decide, not solve now**: Stage 12's
`reduceClauseDatabase`/`reduce_clause_database` operates on one
solver's database today. Once there are `num_threads` local databases
plus a periodically-refreshed shared pool, does the existing
`--alg-params` memory limit apply per-thread (simplest, matches
current semantics literally) or as some aggregate budget across
everything? I'd lean per-thread, but it's a real decision to make
before implementation, not an automatic consequence of the rest of
this design.

No language-specific obstacle in either Go or Rust to any of the
above -- this is an algorithm/protocol question, not a capability
gap. The place the languages diverge in *how* you'd build it: Rust's
ownership rules force an explicit, checked answer to "who owns this
clause, and is it copied or shared" at every hand-off (e.g. `Arc<[Clause]>`
for clauses placed in the shared pool, so multiple threads' local
databases can hold a reference to the same immutable learned clause
without copying it, versus a plainly-owned `Vec<Clause>` for a
thread's own not-yet-shared clauses). Go needs the same discipline
(a shared, mutex-guarded slice, or explicit wholesale copies) but
enforced by convention and caught, if at all, by `go test -race` at
runtime rather than by the compiler.

## Other ideas or issues

A few things I'd raise proactively, since they're not covered by any
of the four sections above but will matter once this gets built:

1. **This breaks the project's main verification technique.** Every
   stage so far (11 through 15 especially) has leaned on exact,
   byte-for-byte parity between the Go and Rust binaries -- same seed
   ⇒ same decisions ⇒ same assignment, same `NumConflicts`, etc. -- as
   a correctness check. Multithreading breaks this at the root: which
   thread finds the answer first (and so which satisfying assignment
   gets returned, for instances with more than one) depends on OS
   scheduling, not just the seed, so exact-output parity between
   languages -- and even between two runs of the *same* binary -- will
   no longer generally hold. The SAT/UNSAT *verdict* must and will
   stay fully deterministic (correctness can't depend on scheduling),
   but the verification methodology needs to shift from "diff the
   exact output" to "same verdict; independently re-check any
   returned SAT assignment against the CNF from scratch." Worth
   knowing now, not discovering mid-implementation.
2. **Per-thread RNG.** `hc`/`ws`/`dfs`'s `SelectVarWeighted` tie-breaking
   currently take one shared `*rand.Rand`/`R: Rng`. A single RNG
   shared across threads is either a data race (Go) or won't compile
   (Rust, since `Rng` methods take `&mut self`) unless mutex-guarded,
   and a mutex there would both bottleneck and reintroduce
   scheduling-dependent nondeterminism through a different door.
   Recommend seeding each thread with its own RNG, deterministically
   derived from the master seed and the thread index, so each
   individual thread's own sequence of random choices stays
   reproducible given (seed, thread count) even though the overall
   wall-clock winner isn't.
3. **Aggregate stats for `--verbose`.** `NumDecisions`/`NumConflicts`
   and friends are single numbers today; with `num_threads`
   independent counters, we need a plan (sum across threads is the
   natural default, maybe with a per-thread breakdown at a higher
   verbosity level) -- small, but worth deciding rather than
   improvising later.
4. **Testing strategy has to change too.** Concurrency bugs (races,
   deadlocks, lost wakeups, the termination-detection race described
   above) are exactly the kind of bug the deterministic, seeded unit
   tests this project has relied on won't catch. `go build
   -race`/`go test -race` should become a standard part of this
   project's Go routine from here on. Rust's type system statically
   rules out data races for safe code, which is a real structural
   advantage, but it doesn't catch logic-level concurrency bugs
   (deadlock, livelock, termination-detection races) in either
   language -- both need dedicated stress tests (many repeated runs,
   or runs with artificially small work units/injected delays to
   shake out ordering-dependent bugs), not just more unit tests of the
   kind already in place.
5. **Core-count discovery, informational only.** Go:
   `runtime.NumCPU()`/`runtime.GOMAXPROCS(0)`; Rust:
   `std::thread::available_parallelism()` (stable since 1.59). Since
   we're deliberately allowing oversubscription, this shouldn't gate
   anything -- but it might be worth a `--verbose` courtesy note like
   "requested 16 threads on an 8-core machine," in both languages.

## Summary of my recommendations

- Allow oversubscription in both languages; it's a non-issue for Go
  by design, and a non-issue for Rust at any thread count a user of
  this program would plausibly request.
- Rust: `std::thread` + `crossbeam-channel`, not tokio, for all of
  `hc`/`ws`/`dfs`/`cdcl` parallelism -- this is CPU-bound work, and
  tokio is the wrong tool for that regardless of oversubscription.
- `hc`/`ws`: full `time_limit` per thread, not divided; a CAS'd
  atomic flag (matching the existing `timeCheckInterval` idiom) for
  the stop signal.
- `dfs`: the outlined design is sound, but steal from the opposite
  end of the stack from local push/pop, and use a centralized (not
  distributed) termination-detection coordinator, prototyped
  separately before full integration.
- `cdcl`: restart-boundary clause sharing is a good, simple first
  cut, but restrict work redistribution to the same
  restart/seed-region granularity, not mid-search decision stacks --
  the reason-pointer coupling makes finer-grained CDCL work-stealing
  unsafe in a way it isn't for `dfs`. Simplify the shared pool with
  subsumption only, never bounded variable elimination.
- Plan now for: verification methodology shifting from exact-diff to
  verdict-plus-independent-recheck, per-thread RNG seeding, aggregate
  `--verbose` stats, and `-race`-enabled Go testing becoming routine.
