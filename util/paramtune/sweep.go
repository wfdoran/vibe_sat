package main

// The coordinate-descent driver: run each --sweep stage in order,
// holding every previously-swept parameter fixed at its winning value
// (docs/internal-parameters.md's design notes explain why coordinate
// descent, not one big joint sweep, is the default here) and every
// not-yet-swept parameter at its built-in default, then fix the
// winning combination from this stage before moving to the next.

import (
	"fmt"
	"io"
)

// candidateResult is one full combination's outcome, kept for the
// final report.
type candidateResult struct {
	stageIndex  int
	combination combination
	score       candidateScore
	err         error // set if this candidate's benchcompare run itself failed (a crash in the harness, not a solver issue)
}

// candidateRunner runs one candidate's benchcompare sweep and returns
// its per-file results -- runCandidate in production, a fake in tests
// (real subprocess execution has no place in a unit test).
type candidateRunner func(cfg sweepConfig, p vibeSatParams) ([]bcFileResult, error)

// runSweep runs every stage in order and returns every candidate's
// result plus the final winning overrides (one value per swept path).
func runSweep(cfg sweepConfig, stages []stage, run candidateRunner, progress func(stageIndex, comboIndex, comboTotal int, c combination)) ([]candidateResult, map[string]string, error) {
	overrides := map[string]string{}
	var all []candidateResult

	for si, st := range stages {
		var stageResults []candidateResult
		for ci, combo := range st.combinations {
			if progress != nil {
				progress(si, ci, len(st.combinations), combo)
			}
			p := defaultParams()
			for path, value := range overrides {
				if err := setByPath(&p, path, value); err != nil {
					return all, nil, fmt.Errorf("applying fixed override %s=%s: %w", path, value, err)
				}
			}
			for _, a := range combo {
				if err := setByPath(&p, a.path, a.value); err != nil {
					return all, nil, fmt.Errorf("stage %d, combination %q: %w", si+1, combo.describe(), err)
				}
			}

			results, err := run(cfg, p)
			cr := candidateResult{stageIndex: si, combination: combo}
			if err != nil {
				cr.err = err
			} else {
				cr.score = scoreOf(results)
			}
			stageResults = append(stageResults, cr)
			all = append(all, cr)
		}

		winner, ok := bestOf(stageResults)
		if !ok {
			return all, nil, fmt.Errorf("stage %d: every candidate failed to run; see errors above", si+1)
		}
		for _, a := range winner.combination {
			overrides[a.path] = a.value
		}
	}

	return all, overrides, nil
}

// bestOf picks the lowest-score candidate among results that ran
// successfully (err == nil).
func bestOf(results []candidateResult) (candidateResult, bool) {
	var best candidateResult
	found := false
	for _, r := range results {
		if r.err != nil {
			continue
		}
		if !found || r.score.less(best.score) {
			best = r
			found = true
		}
	}
	return best, found
}

// printSweepReport writes a human-readable table of every candidate
// tried, grouped by stage, followed by the final winning overrides.
func printSweepReport(w io.Writer, results []candidateResult, overrides map[string]string) {
	currentStage := -1
	for _, r := range results {
		if r.stageIndex != currentStage {
			currentStage = r.stageIndex
			fmt.Fprintf(w, "\n== stage %d ==\n", currentStage+1)
		}
		if r.err != nil {
			fmt.Fprintf(w, "  %-60s FAILED: %v\n", r.combination.describe(), r.err)
			continue
		}
		s := r.score
		fmt.Fprintf(w, "  %-60s unsolved=%d issues=%d total_elapsed=%.3fs (go=%.3fs/%d, rust=%.3fs/%d)\n",
			r.combination.describe(), s.Unsolved, s.Issues, s.TotalElapsed,
			s.TotalGoSecs, s.SolvedGo, s.TotalRustSecs, s.SolvedRust)
	}

	fmt.Fprintln(w, "\n== winning configuration ==")
	if len(overrides) == 0 {
		fmt.Fprintln(w, "  (no parameters swept)")
		return
	}
	for path, value := range overrides {
		fmt.Fprintf(w, "  %s = %s\n", path, value)
	}
}
