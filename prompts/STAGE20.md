# Stage 20

This is another no code prompt.  I want to think through the options
for multi-threaded CDCL.  You can run some experiments in the util
subdirectory if needed to answer questions.

## REPORT16.md

### Shared Clause Database

```
essentially every non-portfolio ("divide and
conquer") parallel CDCL solver shares learned clauses *continuously*
```

Can you implement one with minimal contention?


### Work-stealing

```
work-stealing, done the DFS way, isn't safely
portable to CDCL,
```

I was afraid of this.


### simplifying rules

```
**Simplifying the shared pool: subsumption yes, bounded variable
elimination no.**
```

OK.  I understand

### Database Memory Limit

```
**One more thing to decide, not solve now**:
```

The --alg-params memory limit should be per thread.  Each thread gets
this much.  So, for a shared clause database, it can be num_threads *
memory_limit in total.  If each thread has its own database, we may
need to combine only the short clauses (or "hot" clauses) so when we
broadcast back out, no one exceeds its limit.

## Options

I see four options.  Look them over and tell me which you like
the best.  We can use git branches to try more than one.  

### Option A

Each thread has its own clause database.  At each restart, use BFS to
get num_threads starting partial assignments.  Each thread runs the
single threaded CDCL on its starting partial assignment.  Run until
you hit the restart node limit.  Then combine, simplify, and
distribute the clause databases before the next restart.

This is probably the easiest to implement, but maybe the weakest.
We will probably want to strongly encourage the user to use
luby sequence for more frequent restarts. 

### Option B

Use a shared clause database.  Each thread runs the single-thread
version of CDCL.  All of the parallelism is through the shared
database.  We will probably want some randomness in the variable
phase for the top levels to get some variety.  If any thread gets
SAT or UNSAT, use some mechanism to shut everybody down.  In this
version the restarts do not have to stay in sync.  

I have heard that some chess playing engines use something similar.
They only share the move transposition table.  You might to look at
this work for some hints, tricks, and things to avoid.

### Option C

Use a shared clause database.  At each restart, do a BFS to get the
starting partial assignments for each thread.  If after processing
some minimal number of nodes (say the minimal value in the luby
sequence) and if a certain percentage of the node are out of work, do
a restart.  I have no idea what this percentage should be, 10%, 33%,
50%?  No idea, make it a parameter to optimize later.

The minimum number of nodes per start is to make sure that in an UNSAT
end game, you not constantly restarting.

### Option D

Something I have not thought of, but you think is great.  You outlined
something in the `My recommended resolution` paragraph in REPORT16.md.

## Benchmark

Do you need additional or different SAT problems to test these
options?  If so, give a quick description of what you want (or a url)
and will do my best to get it.

