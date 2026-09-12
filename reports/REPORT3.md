# Report: Stage 3

## Summary

Stage 3 is complete for both the Go and Rust versions of `vibe_sat`:
`--help`/`-h` now prints a full listing of every command line
parameter with a short description, and exits 0, in both languages.

## Design

`--help`/`-h` is special-cased ahead of all other argument handling in
both languages, so that it always works even if the rest of the
command line is incomplete or invalid (e.g. `vibe_sat --help` alone,
with no `--input`/`--algorithm`, still prints help instead of an
error):

- **Go** (`internal/cliargs`): `Parse` scans `argv` for a literal
  `--help` or `-h` token before doing anything else, and if found
  returns immediately with `Args{Help: true}`, skipping tokenizing,
  type conversion, and validation entirely. `cmd/vibe_sat/main.go`
  checks `args.Help` right after a successful `Parse` and, if set,
  prints `cliargs.HelpText()` and exits 0 before reading any file. The
  listing text itself lives in a new `internal/cliargs/help.go`.
- **Rust** (`src/cliargs.rs`): a new `wants_help(&argv)` function
  scans the raw argument vector the same way, and `main` calls it
  *before* calling `Args::parse_from_args` at all — so clap's own
  required-argument checking never even runs when `--help` is present.
  clap's own auto-generated `--help`/`-h` handling is disabled
  (`disable_help_flag = true`) since it's now fully superseded by
  `wants_help`; leaving both active would have meant two
  slightly-different help mechanisms depending on whether clap or our
  own check fired first.

Both languages' help text is hand-written (not generated from clap's
built-in derive, on the Rust side) so that the two languages' output
can be kept in close correspondence, and so the wording is entirely
under our control. The exact text isn't identical between the two
(there was no requirement that it be byte-for-byte identical, the way
error message text wasn't in Stage 2 either), but both list the same
six existing flags plus `--help` itself with equivalent descriptions.

## Stray file cleanup

While checking `git status` before finishing, I found a leftover
compiled binary, `go_src/cmd/vibe_sat/vibe_sat`, dated from Stage 2 —
apparently produced by a `go build ./...` I ran for verification
during that stage without realizing that, unlike building multiple
packages, building a single matched main package without `-o` does
write a binary into the current directory. I removed it and added
`go_src/.gitignore` (mirroring the one already added for
`rust_src/target` in Stage 1) so this can't recur. Apologies for not
catching this sooner — it should have been cleaned up at the end of
Stage 2.

## Testing

- Go: added tests for `--help`/`-h` (long form, short form, and that
  it overrides other invalid/missing arguments) and a test that
  `HelpText()` mentions every flag by name. `go test ./...`, `go vet`,
  and `gofmt -l` are all clean.
- Rust: added equivalent tests for `wants_help` and `help_text()`.
  `cargo test` (57 tests total), `cargo clippy --all-targets -- -D
  warnings`, and `cargo fmt --check` are all clean.
- Manually ran both binaries with `--help`, with `-h` mixed in among
  other garbage arguments, and confirmed normal operation (including
  the existing required-argument error paths when `--help` is *not*
  given) is unaffected.

## Open questions / notes for you

- No language/toolchain version changes needed.
- Per STAGE3.md's "Future" note, both `HelpText()`/`help_text()`
  functions are flagged with a comment to update them whenever a new
  command line parameter is added in a later stage.
