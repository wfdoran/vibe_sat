# Stage 1

## Data structure

Decide on a data structure for holding a SAT problem in memory.  While
we can revisit this later if necessary, it would be good to think
through now what you may need later.

## Problem Ingest

Write a routine to read in .cnf files and populate the SAT data
structure.  If verbose level >= 1, print the filename begin read to
stdout. On error at any verbose level, print an error message to
stdout and exit with a non-zero value.

Write another routine print out to stdout the SAT parameters:
number of variables, number of clauses, and number of literals.
If verbose level >= 1, call this routine on the read in SAT
problem.

On normal exit, exit with 0 value.

## Command Line Parameters

--input=<filename>      -i <filename>
SAT CNF file to read in.
This parameter is required

--verbose=<integer>     -v <integer>
verbose level.  Default is 0. 

