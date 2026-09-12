package cliargs

// HelpText returns the full usage text printed when vibe_sat is
// invoked with --help/-h: a short synopsis followed by a description
// of every command line parameter vibe_sat currently understands.
//
// NOTE for future stages: whenever a new command line parameter is
// added, update this text to describe it too (per STAGE3.md).
func HelpText() string {
	return `Usage: vibe_sat --input=<filename> --algorithm=<string> [OPTIONS]

vibe_sat is a command line SAT solver.

Options:
  --input=<filename>, -i <filename>
        SAT CNF file to read in. Required.

  --verbose=<integer>, -v <integer>
        Verbosity level. Default is 0.

  --algorithm=<string>, -a <string>
        Solving algorithm to use. Required. Only "hc" (a basic
        hill-climbing local search) is currently supported.

  --output=<filename>, -o <filename>
        Where to write a satisfying solution, in DIMACS solution
        format. If not given, the solution is printed to the screen
        when --verbose is at least 1; otherwise it is not written
        anywhere.

  --time-limit-secs=<integer>, -t <integer>
        Optional time limit, in seconds, for the search.

  --alg-params <val1> [<val2> <val3>], -p <val1> [<val2> <val3>]
        Algorithm-specific parameters (1 to 3 integer values). For
        --algorithm=hc, at most one value is accepted: the number of
        random restarts to perform. At least one of --alg-params or
        --time-limit-secs is required when --algorithm=hc.

  --help, -h
        Print this help message and exit.
`
}
