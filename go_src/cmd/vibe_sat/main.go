// Command vibe_sat is a command line SAT solver. The program reads a
// DIMACS CNF file into memory and attempts to solve it with the
// selected algorithm (a basic hill-climb local search, "hc"; WalkSAT,
// "ws"; a complete depth-first search, "dfs"; or conflict-driven
// clause learning, "cdcl"), optionally printing progress and writing
// out a satisfying assignment if one is found.
package main

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"os"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cdcl"
	"vibe_sat/internal/cliargs"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/dfs"
	"vibe_sat/internal/hillclimb"
	"vibe_sat/internal/occurrence"
	"vibe_sat/internal/preprocess"
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

	if args.Help {
		fmt.Print(cliargs.HelpText())
		os.Exit(0)
	}

	problem, err := cnf.ReadDIMACS(args.InputFile, args.Verbose)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if args.Verbose >= 1 {
		cnf.PrintSummary(problem)
	}

	originalNumVars := problem.NumVars
	var preResult *preprocess.Result
	if !args.NoPreprocessing {
		preResult = preprocess.Run(problem, args.Verbose)
		if preResult.Unsat {
			// Preprocessing alone already proves the original problem
			// has no solution, regardless of which algorithm was
			// requested; there is nothing left to search for.
			if args.Verbose >= 1 {
				fmt.Println("UNSAT")
			}
			os.Exit(0)
		}
		problem = preResult.Problem
	}

	switch args.Algorithm {
	case "hc":
		runHillClimb(problem, preResult, originalNumVars, args)
	case "ws":
		runWalkSat(problem, preResult, originalNumVars, args)
	case "dfs":
		runDFS(problem, preResult, originalNumVars, args)
	case "cdcl":
		runCDCL(problem, preResult, originalNumVars, args)
	}

	os.Exit(0)
}

// reconstructedAssignment returns the assignment to write out for a
// found solution: assignment as-is if preprocessing was skipped
// (preResult == nil), or reconstructed back to the original problem's
// variable numbering otherwise (see preprocess.Result.Reconstruct).
func reconstructedAssignment(assignment assign.Assignment, preResult *preprocess.Result) assign.Assignment {
	if preResult == nil {
		return assignment
	}
	return preResult.Reconstruct(assignment)
}

// runHillClimb runs the hill-climbing algorithm against problem using
// the restart/time limits and verbosity level given in args, then
// reports and/or writes out the result. If preResult is non-nil, the
// found assignment is reconstructed back to originalNumVars variables
// before being written out.
func runHillClimb(problem *cnf.Problem, preResult *preprocess.Result, originalNumVars int, args *cliargs.Args) {
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
		writeSolution(reconstructedAssignment(result.Assignment, preResult), originalNumVars, args)
	}
}

// runWalkSat runs the WalkSAT algorithm against problem using the
// tries/max-flips/noise/time-limit settings and verbosity level given
// in args, then reports and/or writes out the result. See
// internal/cliargs/help.go for how args.AlgParams maps onto WalkSAT's
// parameters. If preResult is non-nil, the found assignment is
// reconstructed back to originalNumVars variables before being
// written out.
func runWalkSat(problem *cnf.Problem, preResult *preprocess.Result, originalNumVars int, args *cliargs.Args) {
	lists := occurrence.Build(problem)
	rng := newSeededRand()

	params := hillclimb.WalkSatParams{
		MaxFlipsPerTry: hillclimb.DefaultMaxFlipsPerTry,
		NoisePercent:   hillclimb.DefaultNoisePercent,
	}
	if len(args.AlgParams) >= 1 {
		numTries := int(args.AlgParams[0])
		params.NumTries = &numTries
	}
	if len(args.AlgParams) >= 2 {
		params.MaxFlipsPerTry = int(args.AlgParams[1])
	}
	if len(args.AlgParams) >= 3 {
		params.NoisePercent = int(args.AlgParams[2])
	}
	if args.TimeLimitSecs != nil {
		limit := time.Duration(*args.TimeLimitSecs) * time.Second
		params.TimeLimit = &limit
	}

	result := hillclimb.RunWalkSat(problem, lists, params, rng, args.Verbose)

	if result.Satisfiable {
		writeSolution(reconstructedAssignment(result.Assignment, preResult), originalNumVars, args)
	}
}

// runDFS runs the depth-first search algorithm against problem using
// the optional time limit, SelectVar variant, and verbosity level
// given in args, then writes out the result if a satisfying
// assignment was found. Unlike runHillClimb/runWalkSat, a search that
// exhausts its space without a time limit produces a proven UNSAT
// verdict, not just "not found". Per STAGE6.md, args.AlgParams[0] (if
// given) selects the SelectVar variant: 0 (the default) for the
// weighted heuristic from STAGE5.md, 1 for the cheaper static-order
// heuristic from STAGE6.md. If preResult is non-nil, the found
// assignment is reconstructed back to originalNumVars variables
// before being written out.
func runDFS(problem *cnf.Problem, preResult *preprocess.Result, originalNumVars int, args *cliargs.Args) {
	lists := occurrence.Build(problem)
	rng := newSeededRand()

	var timeLimit *time.Duration
	if args.TimeLimitSecs != nil {
		limit := time.Duration(*args.TimeLimitSecs) * time.Second
		timeLimit = &limit
	}

	variant := dfs.SelectVarWeighted
	if len(args.AlgParams) == 1 {
		variant = dfs.SelectVarVariant(args.AlgParams[0])
	}

	result := dfs.Run(problem, lists, timeLimit, variant, rng, args.Verbose)

	if result.Satisfiable {
		writeSolution(reconstructedAssignment(result.Assignment, preResult), originalNumVars, args)
	}
}

// runCDCL runs the conflict-driven clause learning algorithm (STAGE11.md)
// against problem using the optional time limit, SelectVar variant, and
// verbosity level given in args, then writes out the result if a
// satisfying assignment was found. Like "dfs" (and unlike "hc"/"ws"),
// a search that exhausts its space without a time limit produces a
// proven UNSAT verdict, not just "not found". args.AlgParams[0] (if
// given) selects the SelectVar variant: 0/1 match dfs's own Weighted/
// Fast heuristics, and 2/3 (STAGE13.md) select cdcl's own VSIDS/LRB
// heuristics; unlike dfs, cdcl defaults to VSIDS (cdcl.SelectVarVsids)
// if --alg-params is omitted entirely, per cdcl's own package doc
// comment. args.MemoryLimitBytes (if given, via --alg-params's second
// value: STAGE12.md) bounds the learned-clause database's estimated
// size, past which the least active learned clauses are periodically
// deleted; nil leaves it unbounded, as before Stage 12. If preResult
// is non-nil, the found assignment is reconstructed back to
// originalNumVars variables before being written out.
func runCDCL(problem *cnf.Problem, preResult *preprocess.Result, originalNumVars int, args *cliargs.Args) {
	rng := newSeededRand()

	var timeLimit *time.Duration
	if args.TimeLimitSecs != nil {
		limit := time.Duration(*args.TimeLimitSecs) * time.Second
		timeLimit = &limit
	}

	// STAGE13.md leaves the default SelectVar variant for --algorithm=cdcl
	// up to this implementation. The literature cited in internal/cdcl's
	// package doc comment reports LRB beating VSIDS on SAT Competition
	// instances, but this project's own benchmark comparison
	// (REPORT13.md) found VSIDS clearly ahead of both LRB and Weighted
	// on this project's actual (uniform random 3-SAT) benchmark set --
	// real measurement on the relevant benchmarks wins out over a
	// priori literature reasoning, so VSIDS is the default here
	// (unlike dfs, which keeps its own Weighted default).
	variant := cdcl.SelectVarVsids
	if len(args.AlgParams) >= 1 {
		variant = cdcl.SelectVarVariant(args.AlgParams[0])
	}

	result := cdcl.Run(problem, timeLimit, variant, args.MemoryLimitBytes, rng, args.Verbose)

	if result.Satisfiable {
		writeSolution(reconstructedAssignment(result.Assignment, preResult), originalNumVars, args)
	}
}

// writeSolution writes out a satisfying assignment in DIMACS solution
// format: to args.OutputFile if one was given, otherwise to stdout
// when args.Verbose is at least 1 (and nowhere, per STAGE2.md, if
// neither condition holds).
func writeSolution(assignment assign.Assignment, numVars int, args *cliargs.Args) {
	if args.OutputFile != "" {
		file, err := os.Create(args.OutputFile)
		if err != nil {
			fmt.Println(fmt.Errorf("could not create output file %q: %w", args.OutputFile, err))
			os.Exit(1)
		}
		defer file.Close()
		if err := solution.Write(file, assignment, numVars); err != nil {
			fmt.Println(fmt.Errorf("could not write output file %q: %w", args.OutputFile, err))
			os.Exit(1)
		}
		return
	}

	if args.Verbose >= 1 {
		_ = solution.Write(os.Stdout, assignment, numVars)
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
