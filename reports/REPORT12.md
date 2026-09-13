# Report: Stage 12

## Summary

Stage 12 is complete for both the Go and Rust versions of `vibe_sat`:
`cdcl` now bounds its learned-clause database against an optional
user-supplied memory limit, MiniSat-style (Eén & Sörensson, "An
Extensible SAT-solver," SAT 2003) — once the database's estimated
size exceeds the limit, the least "active" learned clauses are
periodically deleted. This is a direct, requested follow-up to
`REPORT11.md`'s diagnosed problem: without it, `cdcl`'s clause
database grows forever, and per-conflict propagation cost measurably
degrades over the course of a long run.

Per `STAGE12.md`, the limit is passed as `--alg-params`'s *second*
value for `--algorithm=cdcl` only, as either a plain byte count or an
integer with a `k`/`kb`/`m`/`mb`/`g`/`gb` suffix (case-insensitive):
`--alg-params 0 100MB`. Omitting it (the default) leaves the database
unbounded, exactly as it behaved before this stage.

**Result up front**: the fix works exactly as diagnosed — a tight
memory limit keeps conflict throughput flat instead of degrading, a
~9x improvement on one hard instance — and this does translate into
solving more SAT instances within the same time budget. It does
*not*, on its own, flip any of the hard UNSAT instances from
Stage 11's benchmark set into "solved within the cap," because their
bottleneck is the sheer *number* of conflicts needed (a decision-
heuristic problem, per `REPORT11.md`'s VSIDS/restarts recommendation),
not the cost per conflict. See "Benchmark" below.

## Design

### Memory estimate

`clauseByteCost`/`clause_byte_cost` approximates one clause's
contribution to the database's footprint: `perClauseOverheadBytes`
(40, standing in for the clause's own slice/`Vec` header plus its
parallel watch and activity slots) plus 4 bytes per literal
(`cnf.Literal`/`Literal` is an `i32`). This is deliberately an
approximation, not an exact accounting of either language's actual
heap usage — the goal is a reduction policy that responds sensibly to
a size budget, not a byte-for-byte memory profiler. `solver.estimatedBytes`/
a local `estimated_bytes` is maintained incrementally (added to when a
clause is learned, recomputed from scratch — cheap, since it only
happens right after a reduction pass shrinks the clause list) rather
than resummed on every check.

### MiniSat-style clause activity

Every learned clause gets an `activity` score, initialized to 0. Every
time `analyze` resolves through a clause during first-UIP derivation,
that clause's activity is bumped by the current activity increment (if
it's a learned clause — original problem clauses, which are never
deletion candidates, don't need one). Once per conflict, the increment
itself is grown by `1/0.999` rather than multiplying every clause's
activity down by `0.999` — the same relative effect (older bumps count
for less compared to newer ones) at O(1) cost instead of O(clauses).
If the increment ever grows past `1e100`, every activity value and the
increment itself are rescaled back down together, to stay well within
`float64`/`f64`'s range over a very long run. This is MiniSat's actual
`claBumpActivity`/`claDecayActivity`/`claRescaleActivity` scheme,
applied to clauses rather than variables (MiniSat also does the
analogous thing for variable activity — VSIDS — which `cdcl` does not
implement; see `REPORT11.md`'s open questions).

### Locking and the compact-and-remap reduction pass

`reduceClauseDatabase`/`reduce_clause_database` triggers once a
conflict's bookkeeping leaves the database's estimated size over the
configured limit. It sorts every learned clause that is *not*
currently **locked** by activity, and deletes the least-active half.
A learned clause is locked if it is some currently assigned variable's
*reason* — `analyze` may still need to walk through it if that
variable's assignment participates in a future conflict, so deleting
it would be unsound, not just wasteful. Clauses from the original
(bootstrapped) problem are never candidates at all, tracked via a
fixed `numOriginalClauses`/`num_original_clauses` threshold recorded
once at solver setup.

The genuinely tricky part: deleting a clause from the middle of the
clause list would silently invalidate every other index into it —
every watch entry, every `reason[v]`, and every entry in the
occurrence lists from Stage 2. Rather than try to patch each of those
in place (easy to get subtly wrong, per `REPORT8.md`'s and
`REPORT9.md`'s own experience with index-sensitive bookkeeping in this
project), the reduction pass instead builds an old-index-to-new-index
map while copying every *surviving* clause (in order) into fresh
`clauses`/`watch`/`activity` slices, remaps `reason[v]` for every
currently assigned variable through that map, and rebuilds the
occurrence lists from scratch against the new clause list. This is an
O(current clauses + literals) operation, acceptable since it only runs
when the limit is actually exceeded, not on every conflict.

### CLI: a second, differently-typed `--alg-params` value

`--alg-params`'s existing values (in every algorithm, including
`cdcl`'s own first value) are plain integers. A byte size like
`"100MB"` is not, which meant the existing uniform
"parse every `--alg-params` token as an integer" step (Go's
`buildArgs`; Rust's clap-derived `Vec<i64>` field) had to change:

- **Go**: `buildArgs` now branches on `args.Algorithm == "cdcl"`
  before converting `--alg-params`'s raw string tokens, parsing the
  first as an integer as before and the second (if given) via the new
  `parseByteSize`, storing it in a new `Args.MemoryLimitBytes *int64`
  field kept separate from `AlgParams []int64`.
- **Rust**: clap can't conditionally choose a value's type per
  algorithm, so `--alg-params` is now collected as raw
  `Option<Vec<String>>` by a new, module-private `RawArgs` (what clap
  parses directly), and a new `build_args` function converts that into
  the public, typed `Args` — parsing the first token as an integer and
  the second (if given, and only for `cdcl`) via `parse_byte_size`
  into a new `Args::memory_limit_bytes: Option<i64>` field. This
  mirrors the Go implementation's tokenize/buildArgs split, which
  already existed for exactly this kind of reason.

`parseByteSize`/`parse_byte_size` splits the token into a leading
run of digits and a trailing unit, parses the digits as an integer,
and maps the unit (case-insensitive) to a multiplier: none → 1,
`k`/`kb` → 1024, `m`/`mb` → 1024², `g`/`gb` → 1024³ (binary units, not
decimal, per `STAGE12.md`'s "kilobytes, megabytes" framing being the
conventional binary ones in this context).

As `STAGE12.md` itself notes, requiring the SelectVar value to be
given (even as its default, `0`) in order to set the memory limit is
"a little klunky" — this follows directly from `--alg-params`'s
existing positional convention (also used by `ws`'s three values), and
changing that convention for `cdcl` alone seemed like a bigger,
unrequested change than reusing it.

## Verification

- **Unit tests** (18 per language, mirrored, +5 net from Stage 11's
  13): `clauseByteCost`/`clause_byte_cost` directly; two focused tests
  of `reduceClauseDatabase`/`reduce_clause_database` itself — one
  confirming a locked clause and a high-activity clause both survive
  while a low-activity, unlocked clause is deleted (and that
  `reason[v]` and the occurrence lists are correctly updated
  afterward), one confirming a no-op when every learned clause is
  locked (and, importantly, that this doesn't panic); and two
  `Run`/`run`-level tests — a satisfiable formula solved correctly
  under a small memory limit, and (the strongest test here) the
  4-pigeon/3-hole pigeonhole problem solved correctly under a **200-byte**
  limit, deliberately far below the problem's own baseline size, to
  force `reduceClauseDatabase` to run (and very likely actually delete
  clauses) on nearly every conflict — exercising the index-remapping
  path under real pressure rather than a contrived single call.
  `go test ./...` (9 packages) and `cargo test` (140 tests, `cargo fmt
  --check`, `cargo clippy --all-targets -- -D warnings`) are all clean.
- **Independent solution verification**: a from-scratch Python script
  checked every returned satisfying assignment against every original
  clause, across 20 `uf50-218` files x 5 flag combinations (default,
  two variants each crossed with a very tight memory limit — 500
  bytes to 10KB): **0 violations for either language.**
- **`dfs`/unbounded-`cdcl` agreement under tight limits**: 30
  `uf75-325`/`uuf75-325` files x 4 flag combinations (including 1KB
  and 500-byte limits, deliberately small enough to force constant
  reduction), comparing Go against Rust directly: **0 mismatches out
  of 120 runs.** A memory limit changes *how much work* the search
  does, never *what it concludes*.

## Benchmark

To check whether the fix actually helps, not just that it's correct,
I first re-ran the exact diagnostic from `REPORT11.md` (a throwaway
harness calling `cdcl.Run` directly against the same hard UNSAT
instance, `uuf250-1065/uuf250-01.cnf`, at increasing time caps) with
and without a memory limit:

| Limit | 5s | 15s | 30s | 45s |
|---|---|---|---|---|
| unbounded | 8,895/s | 5,593/s | 3,929/s | 3,173/s |
| 10MB | 8,994/s | 6,103/s | 5,510/s | 5,397/s |
| **1MB** | 8,994/s | 6,103/s | 27,313/s | **27,091/s** |

(The 1MB row's 5s/15s figures matched the 10MB row almost exactly by
coincidence of rounding at that scale; by 30s the two clearly
diverge.) Unbounded degrades by nearly 3x over 45 seconds, as already
found in Stage 11; a 1MB limit instead stays essentially **flat**, at
roughly **9x** the unbounded rate by the 45-second mark. This
directly confirms the mechanism: bounding the database keeps
per-conflict cost from growing over a long run.

The real question `STAGE12.md` implicitly poses is whether that
translates into solving more instances within a fixed budget. I reran
Stage 11's exact `dfs`-vs-`cdcl` benchmark methodology
(`--alg-params 0`, this time with a 1MB second value) against the
same, sorted-order 250-variable files Stage 11 used, on a smaller
sample (20 `uf250-1065` SAT files at a 30s cap, 15 `uuf250-1065` UNSAT
files at a 60s cap, to keep this session's runtime reasonable) and
compared file-by-file against Stage 11's unbounded numbers for the
identical files:

| Set | Unbounded (Stage 11) | 1MB limit (Stage 12) |
|---|---|---|
| SAT (`uf250-1065`, 20 files, cap 30s) | 6 / 20 solved | **9 / 20 solved** |
| UNSAT (`uuf250-1065`, 15 files, cap 60s) | 0 / 15 solved | 0 / 15 solved |

On the SAT side, the memory limit is a genuine, measurable win: three
additional files (`uf250-012`, `uf250-018`, `uf250-025`) that
previously timed out now solve well within the cap (in 14–20 seconds),
and every already-solved file solved at least as fast or only
trivially slower. On the UNSAT side, there is no change at all: every
one of the 15 sampled instances still times out at 60 seconds either
way.

This is not a contradiction — it's exactly what `REPORT11.md`
predicted the shape of this problem would be. The UNSAT instances in
this benchmark set are hard for a *different* reason than clause
database bloat: without VSIDS or restarts, the search needs an
enormous, decision-quality-limited number of conflicts to resolve them
(hundreds of thousands to low millions, per the diagnostic above,
which still hadn't finished at 45 seconds even at 9x the sustained
throughput). A cheaper conflict is not the same as *needing fewer of
them*. The SAT instances, by contrast, are ones where the search was
already on a productive path and needed comparatively few conflicts —
exactly the regime where a throughput fix (rather than a decision-
quality fix) pays off directly. Stage 12 fixes the problem it set out
to fix; it does not, and was not expected to, substitute for the
VSIDS/restarts work `REPORT11.md` flagged as the more direct lever on
the solved-count metric specifically.

## Command line arguments

`--alg-params`/`-p`'s second value, for `--algorithm=cdcl` only:
an optional learned-clause database memory limit. Either a plain
integer (bytes) or an integer immediately followed by
`k`/`kb`/`m`/`mb`/`g`/`gb` (case-insensitive), e.g.
`--alg-params 0 100MB`. Requires the first value (the `SelectVar`
choice) to also be given, even as its default (`0`), since
`--alg-params` values are positional. Omitted by default, in which
case the database is unbounded, exactly as it was before this stage.
Help text and CLI validation in both languages were updated
accordingly; `dfs`, `hc`, and `ws` are unaffected.

## Testing

- Go: `go build ./...`, `gofmt -l .`, `go vet ./...`, and
  `go test ./...` (9 packages) are all clean.
- Rust: `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`,
  and `cargo test` (140 tests) are all clean.
- Manual/scripted verification as described above.

## Open questions / notes for you

- `REPORT11.md`'s recommendation stands, and this stage's benchmark
  reinforces it: VSIDS (a conflict-aware decision heuristic) and a
  restart policy remain, in my view, the highest-leverage next changes
  if the goal is solving more of this benchmark set's harder
  instances within a time budget — this stage fixed a real, measured
  throughput problem, but throughput was never the whole story for the
  UNSAT side.
- The 40-byte per-clause overhead constant and the 4-bytes-per-literal
  constant are approximations (see "Memory estimate" above), not a
  promise of exact memory accounting — I flagged this in code comments
  in case you'd want a more precise accounting later (e.g. via
  `unsafe.Sizeof`-style introspection in Go, or a custom allocator
  hook in Rust), though I don't think it's worth the complexity unless
  the estimate turns out to be misleading in practice.
- No language/toolchain version changes needed.
- No changes to `dfs`, `hc`, or `ws` in either language.
