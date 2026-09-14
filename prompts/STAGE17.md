# Stage 17

Let's take a first-step at multi-threading vibe_sat.  Start by
multi-threading --algorithm=hc and -algorithm=ws.

## REPORT16.md

The discussion here was great.  Let me go through the points that
I agree with.  There are other points I agree with, but do not
pertain to the hillclimbs.  We will talk about those in a later
stage.

### Rust Threads

```
**Rust: I'd steer away from tokio for this.** Tokio is an *async I/O*
runtime

My recommendation: plain `std::thread::spawn` -- one real OS thread
per search worker --
```

I agree, let's use std::thread::spawn.  If I understand things
correctly, the OS will then schedule the threads.  A little more
expensive than go's green threads, but if accommodates some
over subscription.

```
 plus the `crossbeam-channel` crate for the
coordination messages 
```

I have use crossbeam in the past and been very happy with its
performance.  I believe it has mpmc channels which we may want.
Please use crossbeam channels.

```
`rayon` is worth keeping in mind too,
```
Let's not use rayon for now.

### Time Limit

```
**Time limit: I'd give each thread the full `time_limit`, not
`time_limit/num_threads`.**
```
I agree.  However, if the user specifies the number of starts,
split those across threads.

### Stop Signal

```
**Stop signal**: I'd use the same "shared flag, checked periodically"

 Go: an `atomic.Bool`. Rust: `Arc<AtomicBool>`.
```

This is very reasonable.  


```
One subtlety worth deciding now rather than discovering later: if two
threads find satisfying assignments at nearly the same moment, you
need a definitive single winner, not just "a flag got set."

(Go:
`atomic.Bool.CompareAndSwap`; Rust: `AtomicBool::compare_exchange`.)
```

I completely agree.  We only want one solution to be reported.


### Verification/Testing

```
1. **This breaks the project's main verification technique.** Every
   stage so far (11 through 15 especially) has leaned on exact,
   byte-for-byte parity between the Go and Rust binaries -- same seed
   ⇒ same decisions ⇒ same assignment, same `NumConflicts`, etc. -- as
   a correctness check.
```

That is a great testing feature in single-processor case.   However,
it is the nature of the multi-threading beast that things happen in
different orders.

If at possible, keep this testing when --num-threads=1, but turn it
off when --num-threads > 1.

### RNG

```
2. **Per-thread RNG.** `hc`/`ws`/`dfs`'s `SelectVarWeighted` tie-breaking
   currently take one shared `*rand.Rand`/`R: Rng`.
```

Using a mutex guarded RNG in the threads could become a bottle neck.

I like your idea of each thread having its own RNG seeded from the
master RNG.

### Aggregate Stats

```
3. **Aggregate stats for `--verbose`.** `NumDecisions`/`NumConflicts`
   and friends are single numbers today;
```

We will have to think about this more carefully when we thread dfs and
cdcl.  For the hillclimbs, the only stat I can think of the current best
found.  You will have to make that a shared atomic variable.  


### Testing

```
`go build
   -race`/`go test -race` should become a standard part of this
   project's Go routine from here on.
```

That would be awesome.  For now let's not worry too much about
stress testing it.  At some (probably much later) stage, we will
return to this.   Please remember this.  When I ask you "What
should we do next?", include this as an item.

### Over subscription Warning

```
`go build
   -race`/`go test -race` should become a standard part of this
   project's Go routine from here on.
```
Good idea.  Print a warning with --verbose >= 1 and --num_threads >= 2 * num_cores.

## Timing Info

With --verbose >= 1, at the very end any execution of vibe_sat,
print the wall clock time used.

## Command Line Parameters

We have a new command line parameter

--num_threads=<integer>   or   -z <integer>

which is the number of threads to use.

--arg-params=<num_starts>

Each thread should do ceil(num_starts/num_treads) starts.

We agree that --time-limit should not be divided by the thread count. 