# Stage 32

I want to revisit the memory issue with depth-first search.

## Memory issue

From REPORT29.md

1. **`dfs` is fundamentally unsafe at this scale.** On a 415 MB,
   17.7M-clause file, `dfs` was OOM-killed after using **12.9 GB** of
   memory and nearly **3 minutes** of wall-clock time — despite
   `--no-preprocessing` and a **3-second** time limit. Root cause: the
   per-branch watch-state cloning `REPORT9.md` flagged as a
   deliberate, scale-sensitive trade-off five years of stage-time ago
   ("if Stage 10+ moves to CDCL... this may be worth revisiting") is
   now a *concretely demonstrated* failure mode, not a theoretical
   one.


I don't understand this.  A depth-first search should use very little
memory.  That said, if I am reading REPORT9.md correctly:

 This project's `dfs` search, since Stage 5, instead uses
an **explicit stack of independent, fully cloned assignment
snapshots** — there is no shared trail and no backtrack-undo step at
all; every branch just gets its own copy of everything it needs.


You are cloning the entire state when you move down the search tree.
Is that correct?

As you elude to, we could keep one partial assignment instead of
cloning it.  And have a stack of variables which got assigned at each
depth.  An array of indices into this stack would record which
variables were assigned at each depth (either by the explicit branch
variable or by BCP).  Then when we backtracking, you set those
variables to UNKNOWN in the partial assignment.

BCP will have to updated to record variables it assigns in
addition to setting the variable in the partial assignment.

Dealing with the work-sharing for the multi-threaded version is a
little annoying but doable.  You will now a "done" depth.
Once you complete the done depth, you are done.  All of the
work above that has been shared/stolen.  


I am not sure how the watched-literals work in this case.  Maybe not
a problem.  If they were UNKNOWN when you start backtracking, they
will stay UNKNOWN.  So, maybe that is not an issue.


## non-monotonic

From REPORT22.md:

20. **Diagnose `dfs`'s non-monotonic UNSAT speedup** past 8-16
    threads (`REPORT19.md`): tree-exhaustion vs. a real scaling limit
    was never disambiguated. Minor, low urgency.

While not as important as the memory issue, if you do any profiling
to help fix the memory issue in DFS, see if you can profile this at
the same time and figure out what is happening here.

## Some Other Reason

Is there some other reason why the memory is blowing up in DFS?


## Action

If you figure what the cause of the extreme memory usage is and see a
way to fix, please do so.

