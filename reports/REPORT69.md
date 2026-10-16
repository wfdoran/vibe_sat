# Report: Stage 69

## What this stage was

`STAGE69.md` asked for (1) a detailed description of the clause database in the
multi-threaded `cdcl`: what each thread keeps privately, what is shared, and how and
when sharing happens; and (2) a list of projects to improve clause sharing: ones
already discussed but not done, new ideas from the literature, and my own.
No code in the repo was changed. Nothing is committed. Items in `low_priority.md`
(chronological backtracking, DRAT, Option D, XOR/Gauss) are left off the lists, as
that file asks.

**The short answer to your confusion:** the threads do **not** share a database.
Each thread has a complete private solver. What is shared is a small *stream* of
short clauses: each thread publishes its learned clauses of at most 8 literals into
its own ring buffer, and every 32 conflicts each thread copies the newest few
clauses from each peer's buffer into its own private database. Everything else is
private. (This is also why `docs/background.md`'s heading "shared clause database"
is misleading.)

## 1. How it is built (Go; Rust is the same design)

Files: `go_src/internal/cdcl/clauseshare.go` and `cdcl.go` (`RunParallel`,
`runLoop`, `addLearnedClause`, `learnAndBackjump`); `rust_src/src/cdcl/clause_share.rs`
and `mod.rs`.

### 1.1 Per thread, private

`RunParallel` starts `N` goroutines (Rust: threads). Each calls `runLoop`, which
calls `newSolver`, so every thread builds its own complete `solver`:

- its own **clause list** (`clauses`): the original clauses, plus every clause this
  thread learned, plus every clause it imported;
- the parallel per-clause arrays: `watch`, `clauseActivity`, `clauseLBD`,
  `clauseUsed` (Stage 67), `chooseWatchPos`;
- its own **watcher lists** and **occurrence lists**;
- its own **trail**, assignment, decision levels and `reason` indices (a reason is an
  index into *that thread's* clause list, which is why clauses are copied rather
  than pointed to);
- its own variable scores (VSIDS / VMTF / LRB), saved and target phases, restart
  state, LBD moving averages, mode-switch state, and RNG;
- its own byte count `estimatedBytes` and its own reduction schedule
  (`nextReduceAt`, Stage 67). The memory limit (`--alg-params` val3) is the same
  number for every thread but is compared against each thread's *own* total, so
  total memory is about `N` times the limit;
- its own **learning**: units it learns become level-0 facts in its own trail only.

Differences between threads are therefore only the strategy assignment (restart
policy, phase policy, VSIDS/VMTF mode by thread index or random draw) and the RNG
stream, plus whatever clauses it imports.

### 1.2 Shared

- The input `*cnf.Problem` (read-only; each thread makes its own copy of the clause
  list headers in `newSolver`; in Go the literal arrays of the original clauses are
  not duplicated. I did not audit that nothing writes to them, but nothing in the
  search needs to, since watches live in the separate `watch` array).
- The `stop` atomic flag. The first thread to finish with a verdict CASes it and the
  others quit at their next loop iteration. A thread's verdict, SAT or UNSAT, is
  final for the whole problem; there is no merging of partial results.
- The memory-limit and parameter values (copies of the same numbers).
- The **export buffers**, the only channel for search information.

### 1.3 The export buffer

One per thread, `exportBuffer`: a ring of **256** slots, each an atomic pointer to an
immutable `sharedClause` (Go `atomic.Pointer`; Rust `arc_swap::ArcSwapOption`), plus
an atomic `writeIdx`. Exactly one thread (the owner) writes; all others read. No
lock, never blocks. `publish` copies the literals into a fresh object, stores it in
slot `writeIdx % 256`, then increments `writeIdx`. Old entries are silently
overwritten. The design is lossy on purpose: a missed clause costs speed, never
correctness.

### 1.4 When a clause is exported

In `addLearnedClause`, immediately after conflict analysis, **if** the new clause
has 2 to 8 literals. That is the whole filter. It is by length only (the exporter's
LBD is not used or sent), and:

- **unit clauses are never exported** (`len == 1` returns before the check);
- **imported clauses are never re-exported** (`importClause` does not publish), so
  there is no echo.

### 1.5 When and how a clause is imported

`learnAndBackjump` ends each conflict with `maybeImport()`, which only does work when
`numConflicts % 32 == 0`. This happens *after* the backjump, so the thread is at
decision level `backtrackLevel`, not necessarily 0. For each of the `N-1` peers:

1. `importWindow(cursor, peer.writeIdx, 256, 8)` picks the range of sequence numbers
   to read: from this thread's cursor for that peer, but never earlier than the
   ring still holds and never more than the **newest 8** (`importMaxPerPeer`,
   Stage 55). The cursor then jumps to `writeIdx`. Older backlog is skipped.
2. Each clause read goes to `importClause`, which:
   - runs `chooseWatch` twice to find two literals that are **not false** under the
     thread's current trail. If it cannot, the clause is **dropped for good** (the
     cursor has already moved past it);
   - otherwise appends a private copy to the clause list with activity 0,
     `LBD = length` (the sender's true LBD is meaningless here), the Stage 67
     "used" counter for that LBD, new occurrence-list and watcher entries;
   - triggers a reduction if that pushes `estimatedBytes` over the memory limit.

After that an imported clause is an ordinary clause: it takes part in propagation
and conflict analysis, can be a reason, is subject to scheduled reduction on the
same terms as a learned one (clauses of length 2 are glue and never deleted;
length up to 6 get the tier-2 protection of 2 reductions; 7 and 8 get the tier-3
protection of 1).

Cost per check is bounded by `(N-1) × 8` clauses. A rare race (a peer overwrites a
slot just before it is read) can import a duplicate. Harmless.

## 2. What the sharing actually does (measured, Go, new)

I put counters in a throwaway copy of the Go code in the scratchpad (the repo is
unchanged). Default settings, `-a cdcl -z N`, one run each, so treat as indicative.

| Run | learned (all threads) | exported | read by importers | accepted | dropped |
|---|---|---|---|---|---|
| uuf250-0100, 4 threads | 179,811 | 37,616 (21%) | 83,034 | 81,853 | 1,181 (1.4%) |
| uuf250-0100, 8 threads | 212,516 | 33,490 (16%) | 187,545 | 183,811 | 3,734 (2.0%) |
| bw_large.d, 8 threads | 6,731 | 2,820 (42%) | 9,520 | 8,582 | 938 (9.9%) |

Reading these:

- **Only 16% to 42% of learned clauses are short enough to export**, and unit
  clauses are almost none of them (16 to 62 per run).
- **Delivery is high, not the bottleneck.** If every export reached every peer,
  reads would be `(N-1) × exported`: 113k expected against 83k at 4 threads, 234k
  against 188k at 8 (about 73% to 80%). The rest are lost to the 8-per-check cap or
  ring overwrite.
- **Imported clauses are a large part of each thread's database.** At 8 threads on
  `uuf250-0100` an average thread learns about 27k clauses and imports about 23k,
  so roughly half its learned database came from peers. The sharing is far from
  marginal, and the Stage 67 reduction therefore matters for it too.
- **Drops are rare on random 3-SAT (1% to 2%) but 10% on the structured file, and
  about half of the binary clauses** on `uuf250-0100` (length 2: 52 of 108
  accepted at 4 threads, 113 of 269 at 8; length 3 about 80% to 82%; length 8 above
  98%). A binary clause is exactly the clause the filter drops most often, and
  binary clauses are the most valuable kind. Not every drop is a loss of value: a
  clause that is all-false or one-literal-left under the importer's trail is
  precisely one that would have propagated or conflicted at once.
- Not measured: how often imported clauses are *used* in conflict analysis
  compared with self-learned ones. That is the number that would say whether
  sharing quality or quantity is the limit, and I do not have it.

Earlier results that bear on this: Stage 21: about 2.8x to 3.0x speedup at 8
threads (random UNSAT), then a drop at 16 on a 10-core machine. Stage 55: fixing the
cursor gave 20% to 56% on `uuf250`, but importing 1, 8, 32 or 256 per check
was within noise, and Rust at 32 threads got monotonically slower with more imports.
Stage 64 found the thread mix (VSIDS/VMTF) 20% to 50% slower than all-VSIDS on
random UNSAT. Stage 61: `hole10` gets *slower* with threads.

## 3. Projects

### 3.1 Discussed before and not done

1. **Unit-clause sharing** (`REPORT21.md`, documented as a simplification, never
   revisited). Units are rare here (16 to 62 per run) but each is a permanent fact
   that costs peers nothing to apply. Needs a separate channel and an import path
   that assigns at level 0, so it requires the importer to be at level 0 (see 3.3
   item 1).
2. **Quality filter on export or import beyond length** (`REPORT20.md` cites the
   community/LBD-sharing paper; `REPORT34.md` set `LBD = length` as a conservative
   stand-in). Never measured against alternatives.
3. **Structured-instance measurement of sharing** (`REPORT55.md` open question 3):
   everything above was tuned on random UNSAT.
4. **Rust slows with more imports at 32 threads** (`REPORT55.md` open question 2),
   never investigated.
5. **Per-thread `--verbose` breakdown** (`todo.md`, "Per-thread --verbose
   breakdown"). The counters in section 2 are exactly this; it would make every
   other item on this list measurable. Cheap.
6. **Clause vivification** (`todo.md` item 7). Relevant here because imported
   clauses are the natural thing to vivify before keeping.
7. **MallobSat-style simplifications** (`REPORT63.md` item 10): identical workers
   with different seeds and phases and sharing doing the pruning; a few local-search
   workers inside `-z N` (we already have `ws`); preprocess in parallel while the
   others already search. `REPORT61.md` found `-z 8` slower than one thread on
   `hole10`.
8. **Bandit-controlled configuration** (`REPORT63.md` item 11). Peripheral to sharing.

### 3.2 From the literature (web search, only abstracts/snippets read, not the papers)

- **MallobSat** (Schreiber & Sanders, JAIR 2024). Their findings, as the search
  results summarize them:
  - the *best* performance came **without** an LBD limit on shared clauses;
  - the received clause's LBD is **incremented** (or reset) rather than trusted, as
    ours is set to length;
  - the sharing *volume* is controlled and adapted: it aims at a target number of
    literals per exchange and adjusts the next limit by how much of the previous
    exchange survived filtering. By contrast our limit is a fixed length of 8;
  - duplicate clauses are filtered (a study of the same system found the share of
    redundantly produced clauses rising from 5% to 34% from one to 16 nodes);
  - the Kissat backend exports every new redundant clause and every unit, including
    those produced by inprocessing, and **imports at decision level zero**, without
    waiting for a conflict count.
  I have not verified these numbers against the paper.
- **HordeSat / ManySAT lineage**: the length-8 cutoff we copied is from there. Later
  work has moved to volume-targeted limits (above).
- **Our own cited sources** (`docs/references.md`): ManySAT, PaInleSS and the
  community/LBD-based sharing paper.

### 3.3 Ideas of my own (not from the literature, as far as I know), ranked

1. **Import at level 0, after restarts, with unit handling.** Today an import
   happens mid-search and a clause that is already unit or false under the trail is
   dropped. Importing only right after a restart (the trail is level 0, so the
   check is exactly "is it satisfied / unit / false at the root") would allow:
   keep it, *propagate it* if unit (a free implication), and *apply units
   directly*. It would also allow unit sharing (3.1 item 1). Risk: restart
   frequency varies a lot by policy (Luby/Glucose vs polynomial), so a thread that
   rarely restarts would import rarely; fall back to a periodic forced check.
2. **Import with propagation instead of dropping.** The cheaper sibling of item 1:
   when a clause has exactly one non-false, unassigned literal, store it and assign
   that literal with the clause as its reason (at the current level); when all
   literals are false, treat it as a conflict. Binary clauses are the main
   beneficiary (about half are dropped now). Correctness-sensitive because it
   changes the trail outside the normal propagate loop.
3. **Raise the export length cap for tier-2 quality.** Exporting by *LBD at the
   sender* (say LBD ≤ 3 or 4 and length up to 20) rather than length ≤ 8, since
   the sender's LBD is known and the true quality signal. We drop the sender's LBD
   today. Easy to measure with the counters. MallobSat's finding that no LBD limit
   was best suggests mixing both (a length cap but not an LBD cap) is the version to
   test.
4. **Rate control instead of fixed 8 per peer per 32 conflicts.** Section 2 shows
   about 75% delivery. A cap proportional to the peers' actual export rate would
   deliver the rest. But Stage 55 found 1, 8, 32, 256 all tied, so I expect little,
   and a flood is a real risk (half the database is already imported at 8 threads).
5. **Import-side admission by usefulness.** Track per sender how often *its*
   imported clauses are used in conflict analysis, and throttle senders whose
   clauses never get used. Needs a per-clause origin tag. This also gives the
   missing measurement noted in section 2.
6. **Reduce imports separately.** Imported clauses currently get activity 0 and
   `LBD = length`. A cheaper alternative: give them the *tier-3* window only
   (one reduction) regardless of length, so a bad import is gone quickly, while a
   used one survives by use. Tiny change; measure with `-z 8` on uuf250 and
   structured sets.
7. **Deduplicate on import.** A cheap hash of sorted literals per thread to skip
   clauses already held. Section 2 does not measure the duplicate rate, so first
   count it; the MallobSat redundancy numbers suggest it grows with threads.
8. **Re-measure the portfolio mix with sharing off.** Whether the value of
   threads is the *clauses* or the *diversity* is still not separated. A
   `--no-sharing` switch (or a counter-only mode) for `-z N` would answer it and
   tell us what an improvement to sharing can be worth at most. I think this is the
   first thing to do.

### 3.4 What I would do first

Item 8 of section 3.3 plus the counters from 3.1 item 5 (one small stage: a
no-sharing switch and per-thread sharing statistics at `--verbose`). It answers
"how much of the multi-thread gain is the sharing" without guessing, and every
other project above is then sized against it. After that, 3.3 items 2 and 1
(import with propagation, then level-0 import with units) are the ones I expect to
matter, in that order because 2 is smaller.

## 4. Caveats

- Section 2's counters are from one Go run per row on one machine; the Rust numbers
  were not measured, though the design is the same.
- The literature claims in 3.2 come from search-result summaries, not from reading
  the papers; treat the specifics as leads. I have added MallobSat to
  `docs/references.md`.
- I did not run the multi-thread benchmarks for any of the ideas above. They are
  proposals only.
- `todo.md` and `low_priority.md` were not touched.
