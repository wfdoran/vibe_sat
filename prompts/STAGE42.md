# Stage 42

Let's do your top pick from REPORT41.md next.  


1. **Auto-tune internal parameters against the 250-variable random set
   specifically** (tweak). You already flagged this as your top pick
   answering `REPORT40.md`'s question, and I agree: `util/paramtune`
   already exists, the benchmark corpus (`benchmark/uf250-1065`,
   `benchmark/uuf250-1065`, 100 files each) already exists, and this
   is exactly the instance class where `REPORT40.md` measured the
   clearest same-family gap. Cheapest real lever on this whole list.


## Questions

- Given the ranking above, should Stage 42 start with the parameter
  sweep (#1, cheapest, most concrete) and the phase-selection tweaks
  (#2), and treat the watch-list rewrite (#3) and XOR elimination (#4)
  as their own larger, separately-scoped future stages once the
  cheaper items are done? That's the sequencing I'd default to absent
  other direction.

I like this order.  I put #1 as the goal of this stage and will
probably follow your list for the next couple of stages. 

- The watch-list representation item now has two independent signals
  pointing at it (your own "bumped up my list" note after
  `REPORT38.md`, and `REPORT40.md`'s concrete numbers) — worth
  scoping as its own dedicated stage soon, or still fine to sequence
  behind the cheaper tweaks first?

I will mix this in at some point. 

- XOR/Gaussian elimination is a genuinely large subsystem — worth a
  small preliminary spike (confirm how many industrial instances in
  the local `sat_comp/2018` corpus actually contain extractable XOR
  structure, before committing to building the extraction+solving
  machinery) rather than committing to the full feature outright?

I am not as interested in this type of SAT problem.  I realize it
is cryptominisat's sweet spot.  Maybe we can return to this in
the far future.  