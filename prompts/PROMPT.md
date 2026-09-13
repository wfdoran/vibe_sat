# SAT solver

Goal: generate command line SAT solvers written in rust and go.

In parallel, we will generate two command line programs, both called
vibe_sat.  One written in rust with its source code in rust_src.  The
other written in go with its source code in go_src.

The functionality of the two versions should be exactly the same.

Part of why I want to do this is to understand strengths and
weaknesses of each language.

## Stages

We are going to do this in stages.  For each stage, I will add a file
STAGEx.md, where x is the stage number, with details on what I want
you to do in the current stage.  Stick tightly to the requests of each
stage.  Do not try to predict what the next stage is going to be and
attempt to get ahead.

## Code Style 

Follow each language's standard project/directory layout conventions.
Both languages have code formatters (go fmt and rustfmt) which should
be followed.  In general follow the standard language style for
variable, subroutine, file names.

Each subroutine must have a comment block with a brief description of
what the subroutine does and what the parameters are used for.  When
in doubt, include more comments than less.  When in doubt, use longer
more descriptive variables names.  Avoid global variables.

## External Packages

For go, try to not use any external packages.  Do not import
any packages from github.

For rust, using crates is inevitable.  Do not include any SAT
specific crates.  I want you to write that part.  

## Tests

Whenever possible add a unit test for every subroutine.  Use the
standard tesing mechanisms which both languages provide.

## Command line arguments

This is going to be a command line program.  We will use command line
arguments to pass information to the program.  Each argument will have
two versions: a long version

  --long-arg-version=<value>

and a short version

  -a <value>

Both the rust and go versions of vibe_sat must use and understand the
same comand line arguments.  For each stage, I will specify any new
argument names and meanings being introduced in that stage.

## Multiple Cores

Eventually, we are going to run with multiple cores.  For go, this
will likely mean using goroutines.  For rust, this will likely mean
using an asynchronous runtime such as tokio.  Be prepared for this.
Make sure any early decisions on data structures and code layout do
not limit our ability to run on multiple cores later.

## Benchmark Problems

The directory benchmark contains several sample SAT problems in the
DIMACS CNF format.  Some are SAT, some are UNSAT.  The note.txt will
tell you which are SAT/UNSAT.

## Deliverables

For each step:
* go source code changes in go_src
* rust source code changes in rust_src
* A REPORTx.md in reports where x is the same as STAGEx.md.
  In this report, give details of what you did and any issues you
  had.

If you are not able to complete a STAGEx.md, indicate this at the top
of REPORTx.md along with what questions you have or addition
information or tools you need.

## git

Do not git commit.  I will review your work after each stage and
commit it.

## Language Version

Currently, I have go1.26.5 and cargo 1.98.0 installed.  If you would
like some feature from a later vesion, please indicate this in
REPORTx.md.  It is easy for me to update. 

## Platform

Assume that the platform is a generic linux x86_64.

## Project Layout

  /            PROMPT.md and STAGEx.md files 
  /go_src      go source code
  /rust_src    rust source code
  /reports     where you writ REPORTx.md files
  /benchmark   sample cnf files

  You may only write to go_src, rust_src, and benchmark.
  You may not alter PROMPT.md, any STAGEx.md, or any
  of the benchmark files.

  For testing and debuging, you may create tmp directories in the root
  of this project.  You should clean them up when you are done.