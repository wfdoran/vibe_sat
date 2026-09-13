# Stage 12

## Learned-clause database management

Since I am worried about it, let's skip ahead and work on this.

7. **Learned-clause database management.** Once CDCL is running for a
   while, thousands of learned clauses accumulate; periodically
   deleting the least "active" ones (MiniSat-style) bounds memory and
   keeps propagation fast. A necessary companion to #3, not useful on
   its own before it.

## Command Line Parameters

To implement this bound, the user will have to pass a memory limit for
how large the database can be.  Use the second --alg_param.  If the
value is just an interger, that is the number of bytes.  Also accept
an integer followed by one of "k", "kb", "m", "mb", "g", "gb" for
kilobytes, megabytes, and gigabytes.  Accept both lower case and upper
case.

  --alg-params 0 100MB

Yes, it is a little klunky that you have to specify the selectvar
method even if it is the default in order to specify the database
memory limit.

If the user does not specify a limit, allow the database to grow
without limit as it currently does.  