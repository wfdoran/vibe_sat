# Stage 24

I move from WSL shell to an ubuntu laptop.  Also I have run

```
sudo sysctl kernel.perf_event_paranoid=1
```

So, I think this is good time improve the testing harness.


Notes from REPORT23.md:


- **This stage's own benchmark harness** (`bench_test.go`) is a small,
  incidental step toward `REPORT22.md`'s item 18 (a standing Go-vs-
  Rust benchmark harness) -- it only covers `cdcl` so far, and only in
  Go (Rust has no equivalent kept around, since the timing
  instrumentation used here was deliberately temporary). Worth
  extending later if that item gets prioritized, not something I'd
  call "done" on the strength of this stage alone.



Notes from REPORT22.md:

18. **A standing Go-vs-Rust benchmark harness.** `REPORT7.md`'s
    original item 11 -- every stage since has used a one-off,
    thrown-away comparison script instead. Nice infrastructure, not
    urgent.



Improve the testing harness for the go and rust versions.

