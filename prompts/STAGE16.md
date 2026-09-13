# Stage 16

No new code in this stage.  In stead, I want to walk through some of
the issues with adding multitreading.  I have lots of questions for
you to research.  

## Threads vs Cores

My plan is add a new command line parameter

  --num_threads=<integer>    -z <integer>

which is the user's desired number of threads.  This may be larger
than the number of physical cores.  We will allow them to
oversubscribe.  Is that ok?

For go, the go runtime allows many more goroutines than hardware
cores.  Go should not be a problem.

I am not as familiar with rust.  I assume we will be using tokio for
the runtime, but if you have a better suggestion let me know.  I think
the tokio runtime can handle more threads than cores.  Let me know if
this is a problem or if you have a better idea for rust.

Also, are the threading models in the two languages close enough that
we can have similar code and structure in each?

## Hillclimbing

For --algorithm=hc and --algorithm=ws, the parallelism is simple:
start --num_threads threads each doing ceil(num_starts/num_threads)
starts

If only a time limit is given, should each thread do time_limit work
or time_limit/num_threads work?  This probably a question for me,
but I would like your opinion.

We will need some mechanism for a thread to indicated that it found a
solution and have the other treads stop.  Maybe some global flag.

## Depth-First Search

I don't want to do a portfolio solver.  I want to do a honest parallel
depth-first search.  Here is a go-based outline.

Step 1: Do a breath-first search on the initial partial assignment
until the stack has num_threads entries (breath-first: pop() off the
top, select var, try 0/1, BCP, push() on the bottom).  Use these as
the seeds for each thread's depth-first stack.

Step 2: Have a shared channel status channel.  When a thread's stack
becomes empty, it sends a "Need work" message in the status channel.
Any other thread with a stack of size 2 or more, can pop off its top
partial assignment and send it to the thread needing work.  Each
thread will have a channel to receive work on which any other thread
can write to.

Again, we will need some mechanism for a thread to declare that it has
found a solution (SAT).  We will also need some way to detect when no
thread has any work left to do (UNSAT).

Do you see any issues with the go version?  Can we do something
similar in rust?


## CDCL

Again, not a portfolio solver.  Same Step 1 as with the depth-first
search to get initial partial assignment on each thread.

There are issues surrounding the conflict clause database.  I propose
that each thread have its own database.  Between each restart, they
are combined, simplified, and then shared.  Something similar to the
initial preprocessor could be used for simplifying the combined set of
conflict clauses.

What do you think of this idea?  Any issues with either go or rust.

## Other Ideas or Issues

Do you have any better ideas?  What potential issues am I missing?





