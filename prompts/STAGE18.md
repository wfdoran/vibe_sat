# Stage 18

In this stage, we are going to multi-tread the depth-first search
algorithm.

## REPORT16.md

More points from REPORT16.md that are relevant to multi-threading
depth-first search.

### double-ended structure

```
**1. Steal from the opposite end the owner works from, not the same
end.**

 The standard fix, used by
essentially every mature work-stealing scheduler (the Chase-Lev
dequeue, which underlies Rust's own Rayon internally) is asymmetric:
the owner works LIFO from one end (push/pop the *bottom*), while
thieves steal FIFO from the *other* end (steal the *top*) -- "push
inserts a task at the bottom... steal tries to remove a task from the
top... local worker LIFO consumption and remote FIFO stealing"
```

This is what I had intended.  Sorry if what I wrote in the STAGE16.md
prompt was unclear.

### Termination

```
**2. Termination detection needs care, but doesn't need a fully
distributed protocol.**
```

I agree this is tricky, but we do have shared memory.  


```
This is fiddly enough that
I'd want a small standalone prototype/spike of just the termination
protocol before wiring it into the real search, rather than finalizing
it purely on paper now.
```

You may create a util subdirectory form the root for such side
projects.  And we can commit these to the repo in case you want to
revisit them later.  Whatever files you leave, I will assume you want
commited.  The one requirement is the code in go_src and rust_src must
be stand-alone.  Neither can depend on any code in util.  util is a
just a play ground for trying things out and learning.


### BFS solves the problem

```
**3. Fewer seeds than threads.**
```

This is possible with small or edge case SAT problems.  The
breath-first search code should have return values like {SAT, UNSAT,
UNKNOWN}.  The vast majority of the time it returns UNKNOWN and you
carry on with the multi-threaded depth-first search.  But with odd
cases which return SAT or UNSAT, exit with that value (BFS solved the
problem).


### max num threads

```
("no more than 16
workers")
```

I don't know where this came from.  I would hope that this code would
scale to 128 threads, maybe more.  AMD Turing CPUs can have 192 cores.


### RUST

```
 `crossbeam-deque` is a ready-made, battle-tested
Chase-Lev work-stealing deque
```

That sounds great.  The crossbeam crate is well-tested.

## Memory Model

In REPORT17.md, you mention using `SeqCst` for memory ordering.  I
wholly support this.  Please continue even in hot code where it might
make sense.  At some future optimization session, maybe we can look
into using acquire/release.  For now and for our sanity, continue to
use sequentially consistent.

Also, the go memory model essentially only supports SeqCst.

## Command Line Parameters

No changes.  Again use

--num-threads=<integer>  and -z <integer>

for the number of threads.
