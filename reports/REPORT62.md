# Report: Stage 62

## What this stage was

`STAGE62.md` asked for three things:

1. Edit the `benchmark/pret` files so each header matches its clause count.
2. Run `vibe_sat` on them to confirm they are UNSAT.
3. Keep the CNF parser strict.

Done. No source code changed; nothing has been committed.

## 1. Header edits

All eight files declared one clause fewer than they contain (the problem Stage
61 found). I changed **only the `p cnf` line** in each, keeping its spacing, and
set the clause count to the number the parser reports finding:

| Files | Old header | New header |
|---|---|---|
| `pret60_25`, `_40`, `_60`, `_75` | `p cnf   60  160` | `p cnf   60  161` |
| `pret150_25`, `_40`, `_60`, `_75` | `p cnf  150  400` | `p cnf  150  401` |

`git diff` confirms exactly 8 lines changed (4 of each), one per file, and
nothing else.

The new counts agree with two independent counts: an `awk` count of the
`0`-terminated clauses, and the number in the parser's own error message before
the edit.

## 2. Verification

I ran `--algorithm cdcl` on all eight files in **both** Go and Rust:
**16 of 16 runs printed UNSAT**, each finishing in well under a second. I also
ran `--algorithm dfs` (Go) on `pret60_25`: UNSAT. (Per Stage 61's baseline these
are trivial; preprocessing plus a short search decides them.)

These files are documented in `benchmark/notes.txt` as "Encoded 2-colouring
forced to be unsatisfiable", all UNSAT, so the verdicts match what the notes say.
I did not independently verify the UNSAT results against another solver
(`minisat` is installed but I did not use it).

## 3. Parser strictness

No parser change. I checked that both languages still reject a mismatch in
either direction, using scratch copies of `pret60_25` with the header set to 160
and to 162:

```
problem line declares 160 clauses but 161 were found     (exit 1, Go and Rust)
problem line declares 162 clauses but 161 were found     (exit 1, Go and Rust)
```

The Go and Rust messages are identical. This supersedes Stage 61's open
question about tolerating an undercounted header: the answer is no.

## 4. Notes

- `benchmark/notes.txt` is unchanged.
- `STAGE62.md` is the only prompt that touched `benchmark/` data files; PROMPT.md
  still says benchmark files may not be altered, and I treated this stage's
  explicit instruction as the exception for these eight files only.
- The scratch copies I made for the strictness check were deleted.
