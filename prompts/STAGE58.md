# Stage 58


From REPORT52.md, let's work on this


- The disclosed `preprocess.Run` gap (Stage 8's own pipeline running
  entirely outside `--time-limit-secs`) is arguably bigger than what
  this stage fixed, but also a bigger lift (subsumption/BVE would each
  need their own deadline threading). Worth its own future stage, or
  is "the search's own defensive re-propagation is now interruptible"
  enough for now, matching how `REPORT35.md` itself left this exact
  question open before?


## Known gaps in REPORT56.md

- **Error text for type errors differs between languages.** Go reports
  encoding/json's message (`json: cannot unmarshal string into Go struct field
  ...`); Rust reports `cdcl.glucoseK must be a number`. Both are errors; range
  errors are identical. Making them identical would need hand-written Go
  checking, which I judged out of scope.

This is ok.


- **Integral floats:** Rust accepts `1e3` for an integer field (it is an
  integer), Go's decoder rejects it. Edge case, not unified.

Again ok

- **No overflow checking** of products such as `lubyBase * lubyTerm` or
  `factor * len(clauses)`; a huge in-range value (e.g. `bveWorkBudgetFactor` near
  2^62) could overflow. The ranges only have lower bounds for these, as you did
  not ask for upper bounds. Want upper caps?

Is there any realistic chance that we would every hit these bounds?

- Only the first violation is reported per load.

That is ok.

