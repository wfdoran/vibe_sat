# Report: Stage 27

## Summary

Stage 27 adds project documentation: a new `docs/` directory with four
Markdown files, linked from `README.md`, per `STAGE27.md`'s explicit
request and `REPORT22.md` item 6 ("README sub-pages: usage, sample
runs, background, references"). No code changed in either language
this stage.

- **`docs/usage.md`** — a "man page": every command line option,
  nicely formatted (tables, headers) rather than `--help`'s plain
  indented text, cross-linked to `background.md` for the *why* behind
  each algorithm/flag.
- **`docs/sample-runs.md`** — worked examples using real files from
  `benchmark/`, ordered from the simplest possible invocation up
  through multi-threading, `--no-preprocessing`, and the project's
  non-random-3-SAT instance families (graph coloring, planning,
  circuit fault analysis).
- **`docs/references.md`** — every paper that had a direct, traceable
  influence on some part of the implementation, grouped by the part of
  the solver it shaped, each with a pointer to the `reports/REPORTx.md`
  where it's discussed.
- **`docs/background.md`** — the biggest of the four: a paragraph or
  few on each topic `STAGE27.md` listed (DFS/DPLL, CDCL, WalkSAT,
  preprocessing, restarts, non-chronological backtracking,
  multi-threaded `dfs`/work-stealing, multi-threaded `cdcl`/shared
  clause database, this project's testing methodology across 26
  stages) plus two more I thought were worth including: a "Go versus
  Rust: what this project actually found" section (the whole point of
  building this twice), and a closing "Other things worth knowing"
  section on the project's own measure-first discipline and its
  determinism guarantees.

Per `STAGE27.md`'s own framing ("Make a good first effort. I expect
that I will want to edit these more later"), these are a solid first
draft, not a claim of completeness -- see "Open questions" below for a
few things I'd flag for your own editing pass.

## Verifying every example actually works

Rather than write `docs/usage.md`/`docs/sample-runs.md` from memory of
what `--help` and past reports say, I built fresh binaries (both
languages) and **ran every command that appears in `sample-runs.md`
before writing it down**, copying the real output into the doc where
shown -- the simplest run, proving UNSAT, `hc`/`ws` with
`--alg-params`, `--verbose=2`'s "new best score" lines, `cdcl` with an
explicit heuristic/restart/memory-limit choice, `--num-threads` for
both `dfs` and `cdcl`, `--no-preprocessing`, `--output`, and the three
structured (non-`uf`/`uuf`) instance families. Cross-checked a
representative handful of these against the Rust binary too (identical
output in every case, modulo one command's output being truncated by
my own `| head -5` in the terminal, not by `vibe_sat` itself). This
matches the project's own stated preference (`docs/background.md`'s
own "testing" section, somewhat recursively) for checking real
behavior rather than describing intended behavior.

## Content decisions worth flagging

- **`docs/usage.md`'s `--alg-params` tables restructure, rather than
  simply reformat, `--help`'s text.** The built-in help text is one
  long, algorithm-keyed block; I split it into one table per algorithm
  (`hc`/`ws`/`dfs`/`cdcl`) since that's how a reader actually consults
  it ("I'm using `cdcl`, what does `val2` mean") rather than how it's
  generated in code.
- **`docs/references.md` only lists papers I could point to a specific
  design decision or implementation detail for**, each with the
  `reports/REPORTx.md` where that connection is made explicit -- I did
  not pad it with tangentially-related literature that was mentioned
  once in a brainstorming stage (`REPORT7.md`/`REPORT22.md`) and never
  actually acted on; those are still listed, but explicitly marked
  "surveyed... not implemented" rather than presented as having shaped
  the code.
- **`docs/background.md`'s restart-strategy section explains the
  "polynomial" vs. "geometric" naming directly**, since `--help`
  itself already has to carry a parenthetical about it and a new
  reader deserves the actual story (`STAGE15.md` asked for "geometric"
  growth using a formula that's actually quadratic; a genuinely
  geometric schedule was added afterward under its own, correct name)
  rather than just the disambiguation.
- **I included a "Go versus Rust" section in `background.md`** even
  though `STAGE27.md`'s own background list doesn't name it explicitly
  -- it's the project's stated top-level goal (`PROMPT.md`: "understand
  strengths and weaknesses of each language"), and I had specific,
  concrete findings to report (the Rust-preprocessing-slower
  root-cause, the lock-free-deque race-detector catch) that seemed
  like exactly what "anything else you think is interesting" was
  inviting.

## Testing

No code changed; `go build ./...`/`go test ./...` and
`cargo build --release`/`cargo test` were run only to produce the
binaries used to verify `sample-runs.md`'s examples (both clean, as
they already were coming into this stage), not because anything in
either language needed re-verifying.

## Command line arguments

None. Purely documentation.

## Open questions / notes for you

- **`docs/background.md` is the longest of the four files by a wide
  margin** (about 375 lines) -- I'd rather it be thorough on a first
  pass than force every topic into a strict single paragraph the way
  `STAGE27.md`'s own list suggested, but if you'd rather it be
  trimmed down (or split further, e.g. a separate page per algorithm),
  that's an easy follow-up now that the content exists.
- I did not add a page specifically walking through `--alg-params`'
  positional-value quirk (you must supply every earlier value to set a
  later one) beyond what `usage.md`'s intro paragraph already says --
  flagging in case you'd like a more visual example of it.
- `docs/references.md`'s "surveyed, not implemented" items
  (chronological backtracking, DRAT proof logging, Adaptive Novelty+)
  are exactly `REPORT22.md`'s still-open backlog items; I didn't
  invent anything new here, just documented what's already been
  discussed.
- No language/toolchain version changes needed.
