# Stage 60

Let's finish off a few things.

- Docs. docs/internal-parameters.md is missing the new parameter ranges.

Please fill these in.


- Preprocessing time. Whether it should count against --time-limit-secs is still undecided. Total wall time can reach 1.1× the limit.

No, preprocessing should not count against the time limit.

- Quadratic unit propagation. It is quadratic on implication chains, in both preprocessing and the search bootstrap. The Stage 57 time guard therefore turned one SAT answer into UNKNOWN. The same applies to pure-literal elimination and the BVE restart scan.

What can we do to fix this?


One more for this stage from REPORT50.md


5. **A `paramtune` sweep over `rephaseIntervalRestarts`/
   `rephaseMaxFlips` (`REPORT43.md`).** Worth resolving with the
   now-mature tuning harness before treating rephasing's "near-tie"
   result as final.

but let's do a sweep over all of the tunable parameters.


