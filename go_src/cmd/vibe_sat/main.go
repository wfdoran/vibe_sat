// Command vibe_sat is a command line SAT solver. This stage of the
// program reads a DIMACS CNF file into memory and attempts to solve
// it with the selected algorithm (currently only a basic hill-climb
// local search), optionally printing progress and writing out a
// satisfying assignment if one is found.
package main

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"os"
	"time"

	"vibe_sat/internal/cliargs"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/hillclimb"
	"vibe_sat/internal/occurrence"
	"vibe_sat/internal/solution"
)

// main is the program entry point. It parses command line arguments,
// reads in the requested DIMACS CNF file, runs the selected solving
// algorithm, reports and/or writes out the result, and exits with
// status 0 on success (whether or not a solution was found) or a
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

	switch args.Algorithm {
	case "hc":
		runHillClimb(problem, args)
	}

	os.Exit(0)
}

// runHillClimb runs the hill-climbing algorithm against problem using
// the restart/time limits and verbosity level given in args, then
// reports and/or writes out the result.
func runHillClimb(problem *cnf.Problem, args *cliargs.Args) {
	lists := occurrence.Build(problem)
	rng := newSeededRand()

	params := hillclimb.Params{}
	if len(args.AlgParams) == 1 {
		numStarts := int(args.AlgParams[0])
		params.NumStarts = &numStarts
	}
	if args.TimeLimitSecs != nil {
		limit := time.Duration(*args.TimeLimitSecs) * time.Second
		params.TimeLimit = &limit
	}

	result := hillclimb.Run(problem, lists, params, rng, args.Verbose)

	if result.Satisfiable {
		writeSolution(result, problem.NumVars, args)
	}
}

// writeSolution writes out a satisfying assignment in DIMACS solution
// format: to args.OutputFile if one was given, otherwise to stdout
// when args.Verbose is at least 1 (and nowhere, per STAGE2.md, if
// neither condition holds).
func writeSolution(result hillclimb.Result, numVars int, args *cliargs.Args) {
	if args.OutputFile != "" {
		file, err := os.Create(args.OutputFile)
		if err != nil {
			fmt.Println(fmt.Errorf("could not create output file %q: %w", args.OutputFile, err))
			os.Exit(1)
		}
		defer file.Close()
		if err := solution.Write(file, result.Assignment, numVars); err != nil {
			fmt.Println(fmt.Errorf("could not write output file %q: %w", args.OutputFile, err))
			os.Exit(1)
		}
		return
	}

	if args.Verbose >= 1 {
		_ = solution.Write(os.Stdout, result.Assignment, numVars)
	}
}

// newSeededRand returns a new pseudo-random source seeded from the
// operating system's cryptographically secure random number
// generator. Using crypto/rand only to seed math/rand/v2 keeps the
// resulting *rand.Rand usable as a plain dependency-injected argument
// (needed for deterministic unit tests elsewhere), while still giving
// each run of the program an unpredictable starting point.
func newSeededRand() *rand.Rand {
	var seedBytes [16]byte
	if _, err := crand.Read(seedBytes[:]); err != nil {
		// Extremely unlikely in practice; fall back to a time-based
		// seed rather than failing the whole program over this.
		now := uint64(time.Now().UnixNano())
		return rand.New(rand.NewPCG(now, now^0x9e3779b97f4a7c15))
	}
	seed1 := binary.LittleEndian.Uint64(seedBytes[0:8])
	seed2 := binary.LittleEndian.Uint64(seedBytes[8:16])
	return rand.New(rand.NewPCG(seed1, seed2))
}
