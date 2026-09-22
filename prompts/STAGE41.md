# Stage 41

While we still have plenty of tasks from REPORT33.md to do, let's take
a brainstorming detour.  Another no code stage.  Based on the results
in REPORT40.md, what are some ways to close the gap on minisat on some
problems?

I see three classes of possible improvements:

- Code improvements.  Profile, find hot spots, try to improve them.

- Tweaks.  Small but possibly meaningful changes to CDCL and it
  variable selection heuristics.  Maybe optimize some internal
  parameters.

- A big algorithmic change.

Please consider the possibilities and rank them.  

## REPORT40.md Questions

- `minisat`'s and `cryptominisat5`'s edge shows up most clearly on
  exactly the two kinds of instance this project's own
  `docs/background.md` "Go versus Rust" section already flags as
  where a from-scratch solver's youth would show: larger random
  instances (where raw core-loop throughput and years of low-level
  tuning compound) and industrial ones (where preprocessing/
  inprocessing sophistication matters most). Given `STAGE39.md`'s
  auto-tuning harness now exists, is a focused tuning pass against
  `minisat` specifically on the 250-variable random set worth a future
  stage, or is closing that gap not a goal of this project (which has
  mostly framed itself as "build and understand a real solver," not
  "match production solver performance")?

Certainly this should be on the list.  I would view it as one of the
tweaks.  Bang for the buck, likely to be one of your top suggestions.

- Want the 60-second cap raised for a follow-up run specifically on
  the 6 instances every solver timed out on, to see whether any of
  them are solvable by anyone given more time, or is 60 seconds a
  reasonable enough budget to call this comparison done?

60 seconds is good for now. 