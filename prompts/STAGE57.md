# Stage 57

Let's go after

4. **Time-limit guard for preprocessing:** worth a dedicated stage now that
   the biggest known overrun is fixed?


Here is what I would suggest: if a time limit is given, limit the
preprocessor to 1/10 of that time.


If no time limit is given, let it go.


You can also do a profile run.  Ideally all of the subroutines should
be O(n log n) in their input size.  Note anything that is O(n^2) or
greater.  We might want to limit those in some way.


