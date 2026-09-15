# Stage 21

A very nice discussion in REPORT20.md.  Let's move forward with
Option B.

## Restart Strategy

You asked

```
`**Which restart strategy would each Option-B thread use?**
```

Let's add a new choice for the second --arg-params (val2) with
--algorithm=cdcl.  When val2=4, the threads will round-robin select
strategy among [2,3,1].  thread0 uses quadratic, thread1 uses
geometric, thread2 uses luby, thread3 uses quadratic, ...  So, each
strategy gets used on roughly the same number of threads.  This might
be a little tricky with goroutines as they don't have a "threadid".  If
this is a problem, go random among {1,2,3}.

With --num-threads=1, quadratic is still the default.

With --num-threads>1, this new method is the default.  

If the user explicitly sets a different restart strategy, even 0, all
threads will use that.

## Benchmarks

I added blocksworld, flat125-301, flat200-479, and ssa to the
benchmark directory.  Use them as you see best.

## Branch

Continue to work on the main branch.  I did however add branch CDCL
at this point so we can go back and try Option D at some point in the
future.



