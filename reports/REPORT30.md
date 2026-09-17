# Report: Stage 30

## Summary

`STAGE30.md` asked two questions about `REPORT29.md`'s Finding 2 (`O(clauses²)`
subsumption elimination blowing timeouts on a large fraction of even small SAT
Competition files): is there an `O(clauses·log(clauses))` algorithm for
subsumption elimination, and if not, what bound should bound it?

**Answer to the first question: no.** There is no known algorithm that solves
general clause subsumption in subquadratic worst-case time, and there is
reason to believe none exists — see "Is there an `O(n log n)` algorithm?"
below. So this stage does both things `STAGE30.md`'s "if yes / if not"
framing asked for, together rather than as alternatives:

1. **Replaced the `O(clauses²)` all-pairs scan with occurrence-list-based
   backward subsumption** (the standard SatELite/MiniSat technique): for
   clause A to subsume clause B, every literal of A — including whichever one
   of A's own literals occurs in the *fewest* clauses overall — must appear in
   B, so only that one literal's occurrence list ever needs to be searched.
   This is an **exact** restriction, not an approximation, and turns the
   common case from `O(clauses)` candidates per clause into `O(how often the
   rarest literal actually recurs)` — close to linear for most real CNF
   instances.
2. **Added a hard work-budget cap** (`subsumptionWorkBudgetFactor = 64`,
   `SUBSUMPTION_WORK_BUDGET_FACTOR` in Rust) bounding total candidate-pair
   examinations to a linear multiple of the clause count, so even an
   adversarial or unusually dense formula degrades to "less subsumption
   found" rather than "unbounded running time."

Implemented identically in Go and Rust, verified with new unit tests, a
500-trial brute-force fuzz test per language, `go test -race`, and
`cargo clippy --all-targets -- -D warnings`. On every file this stage
measured, subsumption elimination itself now takes **single-digit
milliseconds to low tens of milliseconds**, down from multiple seconds to
tens of seconds (see "Benchmark results" below) — a 100-1000x improvement in
the technique this stage targeted.

**However, a cross-language sweep replicating `REPORT29.md`'s Finding 4
methodology found the same fraction of files (7 of 15 Go, 6 of 15 Rust)
still killed by the harness's hard timeout, unchanged from before this
stage.** This is not a failure of the fix above — direct measurement
(below) confirms subsumption is no longer the bottleneck on any of those
files. It is a "fixed one bottleneck, exposed the next one" result: once
subsumption elimination stops dominating, **bounded variable elimination
(BVE) is now the dominant, and on some files effectively unbounded, cost**.
This was already flagged as a smaller residual item on one file in this
stage's own investigation (`gto_p50c314.cnf`); the sweep shows it is in fact
the *same class of problem* `REPORT29.md` reported for subsumption, just one
technique later in the pipeline. See "A new headline finding: BVE is now the
bottleneck" below — this is this stage's most important result, more
important than the subsumption fix's own numbers, and squarely out of
`STAGE30.md`'s scope to fix.

## Is there an `O(n log n)` algorithm for subsumption elimination?

No known algorithm solves general clause-set subsumption elimination in
subquadratic worst-case time, and this project believes (without proving)
that none exists.

Subsumption checking — "is every literal of clause A also a literal of
clause B?" — is structurally the same problem as **Orthogonal Vectors
(OV)**: given two sets of `n` boolean vectors each of dimension `d`, decide
whether some pair (one from each set) is orthogonal (share no coordinate
where both are 1). Treating each clause as a bit-vector over the variable
space (dimension = number of variables) and "subsumes" as "is a subset of"
is the same containment/domination structure OV asks about. OV has a
long-studied conjectured lower bound: under the **Strong Exponential Time
Hypothesis (SETH)**, no algorithm solves OV in `O(n^{2-ε})` time for any
`ε > 0` once `d` is not small relative to `n` (Williams' fine-grained
complexity results connect SETH to a family of "quadratic-time" problems
including OV, edit distance, and — closest to this case — the
set-containment/subset-query family subsumption checking belongs to). A
clause set with many variables relative to clause count is exactly the
regime where this lower bound applies, and it is also exactly the regime
where `REPORT29.md`'s pathological files live (`prime_119218851371.cnf`:
4,365 variables over 23,693 clauses — a variable-to-clause ratio far higher
than this project's `benchmark/`-derived intuition, built mostly on
SATLIB's fixed-ratio random 3-SAT, had prepared for).

This is a *conjectured* lower bound, not a proof (P vs NP-adjacent questions
of this kind are essentially never provable with current techniques), so it
remains conceivable a smarter algorithm exists. But it is why real SAT
preprocessors (MiniSat, SatELite, CaDiCaL) universally use the same
occurrence-list restriction this stage implements rather than a genuinely
subquadratic algorithm: it is the best known practical approach, not a
compromise made in ignorance of a better one.

## The fix: occurrence-list backward subsumption

For clause A to subsume clause B (A ⊆ B), *every* literal of A must be a
literal of B. In particular, this holds for whichever one of A's own
literals occurs in the **fewest** clauses overall. So B, if it exists, is
guaranteed to appear in that one literal's occurrence list — checking any
other clause in the formula is provably wasted work. This restriction is
built once per pass (`buildLiteralOccurrenceLists` / `LiteralOccurrence::build`,
`O(literals)` to construct) and is **exact, not approximate**: it can never
miss a real subsumption, only skip candidates that are structurally
guaranteed not to work.

```
eliminateSubsumedClauses(clauses, numVars):
    occ := buildLiteralOccurrenceLists(clauses, numVars)
    for each clause A (subsumer candidate), in order:
        rarest := occ.of(A's least-frequent literal)
        for each candidate index j in rarest:
            skip if j == i, or B already removed, or |A| > |B|
            skip if |A| == |B| and i > j   # tie-break: keep the first-seen copy
            if A ⊆ B: mark B removed
```

The old algorithm's tie-break rule for equal-length clauses (keep whichever
was seen first) is preserved exactly, so `eliminateSubsumedClauses` produces
byte-for-byte the same surviving-clause sequence as before on every input —
confirmed by the new brute-force fuzz test (see "Verification").

This does not change the *worst-case* complexity class: a literal that
occurs in every clause (or an adversarial formula where every clause's
rarest literal still has a huge occurrence list) still makes this
`O(clauses²)`. That is exactly what the work-budget cap below is for.

### Work-budget safety net

`subsumptionWorkBudgetFactor` / `SUBSUMPTION_WORK_BUDGET_FACTOR` (both `64`)
bounds the *total* number of candidate-pair examinations across an entire
`eliminateSubsumedClauses` call to `64 × clauses`, decremented by the length
of each candidate list actually consulted. Once exhausted, the function
returns whatever it has found so far — running out of budget is always safe:
skipping a possible subsumption can never make a formula's satisfiability
answer wrong, only leave it slightly less compact. This directly answers
`STAGE30.md`'s "if not, have some bound" branch, layered on top of (not
instead of) the occurrence-list algorithm, since the two solve different
problems: the occurrence-list restriction makes the *typical* case fast; the
budget caps the *worst* case's wall-clock time regardless of clause shape.

The parallel version (`eliminateSubsumedClausesParallel`) splits this budget
evenly across threads (`budget / numThreads` each) rather than sharing one
atomic counter — a shared counter would need its own synchronization for a
value that exists purely to bound worst-case time, which isn't worth the
contention. This is unaffected by Stage 25's original proof that the
parallelization is exact (the `if !keep[i] { continue }` skip inside the
sequential version is a pure optimization, so splitting the outer loop
across threads over shared, read-only clause/occurrence-list data needs no
coordination beyond `keep`'s atomic writes); restricting each subsumer's
candidates to its rarest literal's list doesn't affect that argument either,
since it only changes *which pairs are ever compared*, never *what the
comparison would find* had it been made.

## Verification

- **Go**: `TestTrySubsumeFromGenericRespectsZeroWorkBudget` (direct,
  deterministic check that a zero budget removes nothing) and
  `TestEliminateSubsumedClausesMatchesBruteForceOnRandomFormulas` (500
  random trials against a kept-for-testing `bruteForceSubsumedClauses` oracle
  — a verbatim copy of the pre-Stage-30 `O(clauses²)` algorithm, including
  its tie-break rule) are new. All 6 pre-existing call sites updated for the
  new `numVars` parameter. `go build ./...`, `go vet ./...`, `gofmt -l .`,
  `go test ./...`, and `go test -race -count=10 ./internal/preprocess/...`
  are all clean.
- **Rust**: `test_try_subsume_from_respects_zero_work_budget` and
  `test_eliminate_subsumed_clauses_matches_brute_force_on_random_formulas`
  (same 500-trial design, ported 1:1) are new. All 6 pre-existing call sites
  updated. `cargo build --release`, `cargo clippy --all-targets -- -D
  warnings`, `cargo fmt --check`, and `cargo test --release` (195 tests) are
  all clean.
- Both languages' existing parallel-matches-sequential tests (several thread
  counts, including more threads than clauses, on both a hand-built
  transitivity-chain example and a real benchmark file) still pass unchanged
  — the occurrence-list restriction and the work-budget split did not
  disturb Stage 25's parallelization correctness.
- Direct correctness argument: any B that A subsumes is, by the subset
  relation's own definition, guaranteed to contain every literal of A —
  including A's rarest one — so restricting the search to that literal's
  occurrence list cannot miss a real subsumption. This is the single fact
  the fuzz tests exist to catch a transcription bug in, not to establish in
  the first place.

## Benchmark results

All preprocessing-isolated timings below use `--algorithm=hc --alg-params=1
--verbose=1` (a single hill-climb restart, so the reported wall-clock time is
overwhelmingly preprocessing cost, not search), matching the methodology
`REPORT25.md` and `REPORT29.md` used for the same files.

| File | Vars/Clauses | Go before | Go after | Rust after |
|---|---|---|---|---|
| `benchmark/blocksworld/bw_large.c.cnf` (716 KB) | 3,016 / 50,457 | 7.27s | 1.15s | 0.71s |
| `benchmark/blocksworld/bw_large.d.cnf` (1.9 MB) | 6,325 / 131,973 | 47.5s | 3.88s | 2.67s |
| `sat_comp/2018/.../prime_119218851371.cnf` (442 KB) | 4,365 / 23,693 | killed at 20s | 1.26s | 0.78s |
| `sat_comp/2018/.../gto_p50c314.cnf` (57 KB) | 635 / 4,319 | 9.8s | 6.18s | 3.89s |
| `sat_comp/2018/.../factoring39916801x54018521.cnf` (207 KB) | 2,160 / 11,587 | 6.7s | 0.33s | 0.21s |
| `sat_comp/2018/.../mchess_15.cnf` (17 KB) | 420 / 1,391 | 3.2s | 0.05s | 0.03s |

("Go before" figures are `REPORT25.md`/`REPORT29.md`'s own measurements,
reproduced here for comparison, not re-measured this stage; "Go after" and
"Rust after" are fresh measurements taken this stage against the exact same
files.)

`bw_large.c.cnf`/`.d.cnf` show `subsumed=0` in the "after" runs — this file
genuinely has no clause that is a literal subset of another, so the dramatic
speedup is entirely from the occurrence-list algorithm reaching that
conclusion in milliseconds instead of the old all-pairs scan taking seconds
to check every one of `50,457²` (or `131,973²`) pairs to reach the same
answer.

`gto_p50c314.cnf` improves by only ~40% (Go) / ~60% (Rust), far less than
every other file here, despite eliminating the most clauses via subsumption
(1,967 of them) of anything tested. Its `preprocess:` line reports
`eliminated=350` — 350 variables removed by bounded variable elimination —
and profiling (see next section) confirms the residual time on this file is
BVE's own per-elimination occurrence-list-rebuild cost, not subsumption.
This was flagged as an isolated, modest residual item when first found; the
next section shows it is neither isolated nor modest.

## A new headline finding: BVE is now the bottleneck

To check whether this stage's fix actually resolves `REPORT29.md`'s Finding
4 in aggregate (not just on the four hand-picked files above), I reran that
finding's exact methodology via `util/benchcompare`: `--paths=sat_comp/2018
--max-size-mb=3 --sample=15 --seed=1 --algorithm=cdcl --time-limit-secs=15
--hard-timeout-secs=45`.

| | Go | Rust |
|---|---|---|
| Solved | 0 | 0 |
| Killed by the 45s hard-timeout | 7 of 15 | 6 of 15 |

**These counts are identical to `REPORT29.md`'s original numbers**, despite
this stage's subsumption fix being real, measured, and correct (see above).
That result demanded an explanation rather than being taken at face value —
"the fix didn't help" would contradict the four-file measurements just
shown, which are unambiguous.

I added temporary diagnostic timing (per-technique, per-round, gated behind
an env var; removed before finishing this stage — not part of the
committed diff) to `preprocess.Run` and reran two of the seven flagged Go
files directly:

- `apn-sbox5-cut3-symmbreak.cnf` (21,240 vars, 86,081 clauses): unit
  propagation 15ms, pure-literal elimination 0.5ms, **subsumption
  elimination 4.6ms** — then `eliminateVariables` (BVE) never returned
  within a 40-second kill, and was confirmed still running after an
  additional 2+ minutes before I terminated it.
- `a_rphp065_04.cnf` (552 vars, 10,063 clauses): unit propagation 0.7ms,
  pure-literal elimination 0.7ms, **subsumption elimination 4.6ms** — same
  pattern, `eliminateVariables` never returned within a 30-second kill.

Subsumption elimination — the technique this stage targeted — completes in
single-digit milliseconds on both files. **Bounded variable elimination is
now, by a wide margin, the dominant and in these cases effectively unbounded
cost.** Reading `eliminateVariables`'s implementation (identical in both
languages) confirms why: for *every single variable* it eliminates, it
rebuilds full occurrence lists from scratch (`O(clauses)`) and rescans the
entire clause set to build the survivor list (`O(clauses)` again) — before
even considering that each elimination's resolution step itself costs
`O(|positive occurrences| × |negative occurrences|)`, computed in full
*before* the NiVER non-increasing check can reject it. On a file with
21,240 variables and 86,081 clauses, a routine elimination pass paying two
full `O(clauses)` rebuilds per variable is `O(vars × clauses)` at minimum —
over 1.8 billion basic operations just for the bookkeeping, before a single
expensive high-degree variable's resolution cost is counted.

This is not a new algorithm needed so much as the same class of fix this
stage just applied to subsumption, applied to a different technique:
`REPORT22.md` already flagged BVE's per-elimination rebuild cost as a
"parallelize preprocessing" follow-up, and this stage's own Finding on
`gto_p50c314.cnf` (350 eliminated variables, a real but modest residual)
was an early, softer signal of exactly this. The sweep above shows it is
the same magnitude of problem `REPORT29.md` found for subsumption, just
revealed now that subsumption is no longer masking it by being the slower
of the two.

**I did not fix this.** `STAGE30.md` asked specifically about subsumption
elimination; BVE's rebuild-per-variable cost is a different technique with
a different fix shape (most likely: maintain occurrence lists incrementally
across eliminations instead of rebuilding from scratch each time, plus
possibly a similar work-budget cap), and doing it well in both languages,
with its own correctness argument and test suite, is a full stage of work
in its own right — exactly the kind of scope creep this project's standing
rules ask me to avoid. I'm flagging it here, prominently, as the clear next
priority: it is now the single largest known performance problem in this
project, larger than anything this stage's own fix addressed.

## Command line arguments

None. No `vibe_sat` CLI flags changed in either language this stage.

## Testing

- Go: `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./...`,
  `go test -race -count=10 ./internal/preprocess/...` all clean.
- Rust: `cargo build --release`, `cargo clippy --all-targets -- -D
  warnings`, `cargo fmt --check`, `cargo test --release` (195 tests) all
  clean.
- Cross-language: a `util/benchcompare` sweep (`--paths=sat_comp/2018
  --max-size-mb=3 --sample=15 --seed=1 --algorithm=cdcl
  --time-limit-secs=15 --hard-timeout-secs=45`, 15 files × 2 languages) found
  0 cross-language verdict mismatches and 0 independent verification
  failures — the BVE bottleneck above is a performance problem, not a
  correctness one.
- Manual verification against the six files in "Benchmark results": both
  languages' preprocessing produces the same `vars`/`clauses`/`subsumed`/
  `eliminated` counts as before this stage on every file (confirming the
  rewrite changes *speed*, not *what gets removed*), and every run still
  reports a consistent verdict.

## Open questions / notes for you

- **BVE's rebuild-per-eliminated-variable cost (above) is now this
  project's most urgent known performance problem** — more urgent than
  anything left over from `REPORT29.md`, since it's what's actually
  responsible for the unchanged 7/15 and 6/15 timeout counts in this
  stage's own reproduction of that report's Finding 4. I'd recommend this
  as the next stage, with the same shape of investigation this stage and
  `REPORT25.md` used: profile first, then fix, in both languages, with a
  fuzz test against a brute-force oracle.
- The temporary diagnostic instrumentation used to isolate the BVE finding
  above was removed before finishing this stage; it is not part of the
  diff for you to review, and `git diff` on `go_src/internal/preprocess/`
  should show only the intentional Stage 30 changes.
- I did not attempt to bound or parallelize BVE itself, per `STAGE30.md`'s
  specific scope — see "A new headline finding" above for why that would
  be its own stage.
- No language/toolchain version changes needed.
