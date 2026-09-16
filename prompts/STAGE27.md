# Stage 27

Let's improve the documentation in this stage.  I propose we create a
docs subdirectory where we put several new markdown files which are
linked to in README.md.

Make a good first effort.  I expect that I will want to edit these
more later.

## usage

Basically the same info that --help gives but nicely formatted in
markdown.  In other words a "man page". 

## sample runs

To help the user get started, several sample runs using the files in
benchmark for input.  Give a brief description of what each run does.
Start with the simplest possible run and then high light additional
features.  We don't have to have examples of every possible feature in
the package.

## References

List any papers that played a fundamental role in your implementations
and decisions.

## background

Not sure exactly what I want here.  Maybe a paragraph on each of the
following
- DFS/DLPP
- CDCL
- WalkSAT
- multi-threaded DFS, work stealing
- multi-threaded CDCL, share conflict database
- restart strategies
- non-chronological backtracking
- the prepossessing
- some info the testing you do at each stage?
- anything else you think is interesting

