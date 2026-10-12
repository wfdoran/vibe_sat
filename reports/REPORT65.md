# Report: Stage 65

## What this stage was

`STAGE65.md` asked for two things:

1. Remove the round-robin option for the variable-selection heuristic, even
   with multiple threads, and make VSIDS the default for CDCL again.
2. Clean up `vibe_sat -h`: remove every reference to `STAGEx.md` and
   `REPORTx.md`, which users will not have.

Both are done in Go and Rust. Nothing has been committed. Go and Rust behave
identically.

## 1. Round-robin removed

STAGE64's `--alg-params` `val1 = 5` (round-robin over VSIDS, VMTF, LRB, VSIDS,
VMTF) is gone, along with its rotation table and the per-worker resolution code.
What remains of STAGE64 is VMTF as `val1 = 4`.

- **Default:** VSIDS (`val1 = 2`) for every thread count. Every worker in a
  multi-threaded run uses the same heuristic. Restarts (`val2 = 5`) and phases
  (`val4 = 3`) are still rotated across workers by default, as before STAGE64.
- **`val1` range:** back to 0 to 4. A `val1` of 5 is now an error, with the same
  text in both languages: `for --algorithm=cdcl, the first --alg-params value
  must be 0, 1, 2, 3, or 4 (selecting which SelectVar heuristic to use)`.
- **Tests:** the round-robin mapping test and the 5-thread round-robin test were
  removed, and the CLI range tests now accept 0 to 4 and reject 5, 6 and -1. I
  added a multi-threaded VMTF test (4 threads, one UNSAT and one SAT formula) in
  both languages, and the VMTF fuzz test now covers only VMTF.
- **Docs:** `docs/usage.md` (the `val1` row, and the round-robin paragraph, which
  now says the heuristic is deliberately not rotated) and `docs/background.md`
  ("It is the default").
- **Not changed:** the old reports (`REPORT64.md` and earlier) and the `STAGE*.md`
  prompts still describe the round-robin, as history.

Checked on the built binaries, in both languages: `-z 4` and `-z 1` both print
`select_var=2`; `-p 4` and `-z 3 -p 4` print `select_var=4`; `-p 5` is rejected.

## 2. Help cleanup

`vibe_sat -h` printed 322 lines (Go) and 320 (Rust), with about 40 references to
`STAGEx.md`, `REPORTx.md` or `reports/`. It now prints **295 lines with none**. I
rewrote the text once and put the same text in both languages.

What changed besides removing the references:

- **Project history removed.** For example "this was val2 before STAGE15.md added
  the restart strategy", "STAGE44.md numbers this 5 (moved from 4)", "originally,
  incorrectly, called geometric", and the passages that justified a default by
  citing a report's benchmark comparison. The defaults are still stated, without
  the justification.
- **Facts kept.** Every option, every `--alg-params` value, every default, the
  parameter ranges for `--internal-params`, the one-tenth preprocessing time
  rule, and the notes about `_`, oversubscription and the memory limit.
- **Small corrections.**
  - `--time-limit-secs` now says it is optional for `cdcl` as well as `dfs`
    (that was already true; only `dfs` was mentioned).
  - The `cdcl` thread description says every thread uses the same
    variable-selection heuristic.
  - The `--internal-params` text now says a file named explicitly that is missing
    or invalid is a hard error (the Rust text already said this; the Go text did
    not).
  - Literature names (Luby, Audemard & Simon, Chanseok Oh) are kept.
- **The two languages' help text had drifted apart.** Before this stage the Go and
  Rust `-h` outputs differed in four places (the `--internal-params` and
  `--reset-internal-params` paragraphs, and two lines in the `val2`/`val4`
  notes). They are now byte-identical (checked with `cmp` on the real output of
  both binaries), and each file's doc comment says it must stay word for word the
  same as the other.
- **Still referenced:** `docs/internal-parameters.md` (a user-facing doc in the
  repository, not a stage or report file). Say if you would rather the help not
  mention it either.

I also checked the other user-visible strings in both languages (error messages
and the like): none cites a stage or report file. Source-code comments still do,
as before; you asked only about the help.

## 3. Verification

- Go: `gofmt -l` clean, `go vet ./...` clean, `go test ./...` passes,
  `go test -race ./internal/cdcl ./internal/cliargs` passes.
- Rust: `cargo fmt --check` clean, `cargo clippy --release --all-targets --
  -D warnings` clean, `cargo test --release`: 327 passed (328 before: two
  round-robin tests removed, one multi-thread VMTF test added).
- `vibe_sat -h` is identical in both languages and contains no `STAGE`, `REPORT`
  or `reports/`.
- Not rerun: the benchmark sweeps and the Stage 55 fuzz. Nothing about the search
  itself changed apart from which heuristic a multi-threaded run uses by default,
  which is the VSIDS behavior measured in `REPORT64.md` (section 2, the "all-VSIDS"
  column).

## 4. Notes

- The "single-thread default" wording in the code comments and docs is now just
  "the default".
- `todo.md` and `low_priority.md` were not touched.
