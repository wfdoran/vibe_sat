// Package cliargs parses the command line arguments shared by the
// vibe_sat program. Every argument supports both a long form
// (--long-name=value) and a short form (-x value); both forms write
// into the same field so either spelling may be used interchangeably.
package cliargs

import (
	"flag"
	"fmt"
	"io"
)

// Args holds the parsed command line arguments for vibe_sat.
type Args struct {
	InputFile string // --input / -i : path to the DIMACS CNF file to read (required)
	Verbose   int    // --verbose / -v : verbosity level, default 0
}

// Parse parses argv (typically os.Args[1:], i.e. excluding the program
// name) into an Args structure. It returns an error if argv cannot be
// parsed, or if a required argument (--input/-i) is missing.
func Parse(argv []string) (*Args, error) {
	fs := flag.NewFlagSet("vibe_sat", flag.ContinueOnError)
	// Suppress the flag package's default usage output; callers are
	// responsible for reporting the returned error themselves.
	fs.SetOutput(io.Discard)

	args := &Args{}

	const inputUsage = "SAT CNF file to read in (required)"
	fs.StringVar(&args.InputFile, "input", "", inputUsage)
	fs.StringVar(&args.InputFile, "i", "", inputUsage)

	const verboseUsage = "verbose level (default 0)"
	fs.IntVar(&args.Verbose, "verbose", 0, verboseUsage)
	fs.IntVar(&args.Verbose, "v", 0, verboseUsage)

	if err := fs.Parse(argv); err != nil {
		return nil, err
	}

	if args.InputFile == "" {
		return nil, fmt.Errorf("missing required argument: --input=<filename> (or -i <filename>)")
	}

	return args, nil
}
