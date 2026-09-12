// Command vibe_sat is a command line SAT solver. This stage of the
// program reads a DIMACS CNF file into memory and, when run with a
// verbose level of 1 or higher, prints basic statistics about the
// problem that was read.
package main

import (
	"fmt"
	"os"

	"vibe_sat/internal/cliargs"
	"vibe_sat/internal/cnf"
)

// main is the program entry point. It parses command line arguments,
// reads in the requested DIMACS CNF file, optionally prints summary
// statistics about it, and exits with status 0 on success or a
// non-zero status if any error occurs.
func main() {
	args, err := cliargs.Parse(os.Args[1:])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	problem, err := cnf.ReadDIMACS(args.InputFile, args.Verbose)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if args.Verbose >= 1 {
		cnf.PrintSummary(problem)
	}

	os.Exit(0)
}
