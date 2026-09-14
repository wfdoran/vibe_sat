package hillclimb

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"vibe_sat/internal/assign"
	"vibe_sat/internal/cnf"
	"vibe_sat/internal/occurrence"
)

// DefaultMaxFlipsPerTry and DefaultNoisePercent are the values used
// for WalkSatParams.MaxFlipsPerTry and WalkSatParams.NoisePercent
// when the corresponding --alg-params value is not given on the
// command line.
const (
	DefaultMaxFlipsPerTry = 10000
	DefaultNoisePercent   = 50
)

// WalkSatParams configures a single call to RunWalkSat.
//
// NumTries is the number of random restarts to attempt (nil means
// unlimited, relying on TimeLimit instead to decide when to stop).
// MaxFlipsPerTry bounds how many flips a single try may make before
// giving up and starting a fresh try; it is never nil, defaulting to
// DefaultMaxFlipsPerTry. NoisePercent is the probability, as a
// percentage from 0 to 100, of flipping a uniformly random variable
// of the chosen unsatisfied clause instead of the one that breaks the
// fewest other clauses; it is never nil, defaulting to
// DefaultNoisePercent.
//
// Stop and BestScore (STAGE17.md) play exactly the same role here as
// they do in Params -- see its doc comment -- and are nil for every
// single-threaded caller, including RunWalkSat itself.
type WalkSatParams struct {
	NumTries       *int
	MaxFlipsPerTry int
	NoisePercent   int
	TimeLimit      *time.Duration
	Stop           *atomic.Bool
	BestScore      *atomic.Int64
}

// RunWalkSat performs a WalkSAT search (Selman, Kautz & Cohen, 1994):
// repeatedly start from a random complete assignment, then repeatedly
// pick a currently unsatisfied clause at random and flip one of its
// variables -- with probability params.NoisePercent/100 a uniformly
// random variable of the clause ("noise", letting the search take a
// random walk step), and otherwise whichever variable of the clause
// would break the fewest currently-satisfied clauses ("greedy") --
// until either every clause is satisfied or params.MaxFlipsPerTry
// flips have passed without success, in which case a new random
// assignment is tried.
//
// Unlike the simple hill-climb in Run, WalkSAT always commits the
// chosen flip, even when it makes the score worse; this, combined
// with the noise parameter, is what lets it escape local optima that
// trap the simple hill-climb. The restart/time-limit handling, the
// Result returned, and the meaning of the verbose levels are
// otherwise identical to Run; see its documentation for details.
func RunWalkSat(problem *cnf.Problem, lists *occurrence.Lists, params WalkSatParams, rng *rand.Rand, verbose int) Result {
	if verbose >= 1 {
		fmt.Println("walksat:", describeWalkSatParams(params, 0))
	}
	result := walkSatLoop(problem, lists, params, rng, verbose)
	if verbose >= 1 {
		fmt.Println(verdict(result.Satisfiable))
	}
	return result
}

// RunWalkSatParallel runs the same search as RunWalkSat, split across
// numThreads concurrent workers (STAGE17.md), exactly as RunParallel
// does for Run -- see its doc comment for the full behavior (start
// splitting, unshared time limit, per-worker RNG derivation, and the
// single-winner stop/report protocol); everything there applies here
// unchanged, just for WalkSAT's NumTries instead of NumStarts.
func RunWalkSatParallel(problem *cnf.Problem, lists *occurrence.Lists, params WalkSatParams, numThreads int, rng *rand.Rand, verbose int) Result {
	if numThreads <= 1 {
		return RunWalkSat(problem, lists, params, rng, verbose)
	}

	if verbose >= 1 {
		fmt.Println("walksat:", describeWalkSatParams(params, numThreads))
	}

	subRands := make([]*rand.Rand, numThreads)
	for i := range subRands {
		subRands[i] = newSubRand(rng)
	}

	var stop atomic.Bool
	var bestScore atomic.Int64
	bestScore.Store(-1)
	var winner atomic.Int32
	winner.Store(-1)

	results := make([]Result, numThreads)
	var wg sync.WaitGroup
	for i := 0; i < numThreads; i++ {
		workerParams := params
		workerParams.Stop = &stop
		workerParams.BestScore = &bestScore
		if params.NumTries != nil {
			n := ceilDiv(*params.NumTries, numThreads)
			workerParams.NumTries = &n
		}

		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = walkSatLoop(problem, lists, workerParams, subRands[i], verbose)
			if results[i].Satisfiable && stop.CompareAndSwap(false, true) {
				winner.Store(int32(i))
			}
		}(i)
	}
	wg.Wait()

	final := Result{}
	for _, r := range results {
		final.Starts += r.Starts
	}
	if idx := winner.Load(); idx >= 0 {
		final.Satisfiable = true
		final.Assignment = results[idx].Assignment
		final.Score = results[idx].Score
	}

	if verbose >= 1 {
		fmt.Println(verdict(final.Satisfiable))
	}
	return final
}

// walkSatLoop is RunWalkSat's search loop, without the "walksat:
// ..."/"SAT"/"UNKNOWN" announcements -- see runLoop's doc comment for
// why (RunWalkSatParallel prints one combined announcement/verdict
// instead of one per worker).
func walkSatLoop(problem *cnf.Problem, lists *occurrence.Lists, params WalkSatParams, rng *rand.Rand, verbose int) Result {
	startTime := time.Now()
	numClauses := problem.NumClauses()
	bestScore := -1
	recordIfBest := func(score int) {
		if params.BestScore != nil {
			for {
				old := params.BestScore.Load()
				if int64(score) <= old {
					return
				}
				if params.BestScore.CompareAndSwap(old, int64(score)) {
					if verbose >= 2 {
						fmt.Printf("new best score: %d/%d\n", score, numClauses)
					}
					return
				}
			}
		}
		if score > bestScore {
			bestScore = score
			if verbose >= 2 {
				fmt.Printf("new best score: %d/%d\n", score, numClauses)
			}
		}
	}

	tries := 0
	for {
		if params.Stop != nil && params.Stop.Load() {
			break
		}
		if params.NumTries != nil && tries >= *params.NumTries {
			break
		}
		if params.TimeLimit != nil && time.Since(startTime) >= *params.TimeLimit {
			break
		}
		tries++

		assignment := assign.NewRandom(problem.NumVars, rng)
		state := newClimbState(problem, lists, assignment)
		recordIfBest(state.score)

		for flips := 0; state.score != numClauses && flips < params.MaxFlipsPerTry; flips++ {
			clauseIndex := state.unsatClauses[rng.IntN(len(state.unsatClauses))]
			v := chooseFlipVariable(state, clauseIndex, params.NoisePercent, rng)
			state.flip(v)
			recordIfBest(state.score)
		}

		if state.score == numClauses {
			return Result{Satisfiable: true, Assignment: state.assignment, Score: state.score, Starts: tries}
		}

		if verbose >= 3 {
			fmt.Printf("gave up after %d flips at score %d/%d (try %d)\n", params.MaxFlipsPerTry, state.score, numClauses, tries)
		}
	}

	return Result{Satisfiable: false, Starts: tries}
}

// chooseFlipVariable picks which variable to flip next, given that
// clauseIndex names a currently unsatisfied clause of state: with
// probability noisePercent/100, a uniformly random variable of the
// clause; otherwise, whichever variable of the clause has the
// smallest break count (see climbState.breakCount), with ties broken
// uniformly at random.
func chooseFlipVariable(state *climbState, clauseIndex int, noisePercent int, rng *rand.Rand) int {
	clause := state.problem.Clauses[clauseIndex]

	if noisePercent > 0 && rng.IntN(100) < noisePercent {
		lit := clause[rng.IntN(len(clause))]
		return lit.Var()
	}

	bestVar := -1
	bestBreakCount := -1
	tieCount := 0
	for _, lit := range clause {
		v := lit.Var()
		breakCount := state.breakCount(v)
		switch {
		case bestVar == -1 || breakCount < bestBreakCount:
			bestVar, bestBreakCount, tieCount = v, breakCount, 1
		case breakCount == bestBreakCount:
			tieCount++
			// Reservoir sampling: keep the new candidate with
			// probability 1/tieCount, so that every tied candidate
			// seen so far remains equally likely to be selected.
			if rng.IntN(tieCount) == 0 {
				bestVar = v
			}
		}
	}
	return bestVar
}

// describeWalkSatParams formats the configured limits/noise for the
// "walksat:" announcement printed at verbose level 1.
// describeWalkSatParams formats the configured limits/noise for the
// "walksat:" announcement printed at verbose level 1. numThreads is
// the worker count to report (RunWalkSatParallel's num_threads=N
// prefix), or 0 for RunWalkSat's single-threaded case, which omits
// the prefix entirely rather than printing "num_threads=0".
func describeWalkSatParams(params WalkSatParams, numThreads int) string {
	description := fmt.Sprintf("max_flips_per_try=%d noise_percent=%d", params.MaxFlipsPerTry, params.NoisePercent)
	if numThreads > 0 {
		description = fmt.Sprintf("num_threads=%d %s", numThreads, description)
	}
	if params.NumTries != nil {
		description += fmt.Sprintf(" num_tries=%d", *params.NumTries)
	}
	if params.TimeLimit != nil {
		description += fmt.Sprintf(" time_limit_secs=%d", int(params.TimeLimit.Seconds()))
	}
	return description
}
