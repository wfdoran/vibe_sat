package main

// A minimal, independent decoder for util/benchcompare's --json
// output (see benchcompare/runner.go's runOutcome and
// benchcompare/report.go's fileResult/writeJSON) -- paramtune only
// needs a handful of fields out of that report to score a candidate,
// so only those are reproduced here rather than importing
// benchcompare's package main (which, being "package main" in a
// different module, isn't importable anyway).

import (
	"encoding/json"
	"os"
)

type bcRunOutcome struct {
	Verdict        string  `json:"verdict"` // "SAT", "UNSAT", "UNKNOWN", or "NONE"
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	ExitErr        string  `json:"exit_err"`
	TimedOutOS     bool    `json:"timed_out_os"`
	SolutionErr    string  `json:"solution_err"`
	Solved         bool    `json:"solved"`
}

type bcFileResult struct {
	File string       `json:"file"`
	Go   bcRunOutcome `json:"go"`
	Rust bcRunOutcome `json:"rust"`
}

func readBenchcompareJSON(path string) ([]bcFileResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var results []bcFileResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// candidateScore summarizes one candidate's benchcompare sweep into
// the numbers a sweep stage ranks candidates by. Lower is better for
// every field.
type candidateScore struct {
	Unsolved      int     // files where either language failed to reach a definite (SAT/UNSAT) verdict -- correctness/completeness comes first
	Issues        int     // cross-language verdict mismatches + failed independent solution verifications -- a hard correctness red flag, reported but does not by itself change ranking beyond Unsolved (a config that "solves" everything but disagrees with itself is still very wrong; see scoreOf's doc comment)
	TotalElapsed  float64 // sum of ElapsedSeconds across every solved (definite-verdict) run, both languages
	SolvedGo      int
	SolvedRust    int
	TotalGoSecs   float64
	TotalRustSecs float64
}

// scoreOf reduces a full benchcompare sweep into a candidateScore.
// Unsolved and Issues are folded into one lexicographic key ahead of
// TotalElapsed deliberately: STAGE39.md's whole premise is that a
// tuning sweep should never be allowed to "win" by making the solver
// less correct or less complete, only faster at doing the same job --
// see docs/internal-parameters.md's design notes and this project's
// long-standing "measure, don't assume" culture (reports/REPORT*.md
// throughout). A config that finishes fewer files, or disagrees with
// itself cross-language, is worse than one that is merely slower,
// full stop; TotalElapsed only ever breaks a tie between two
// candidates that are equally correct and complete.
func scoreOf(results []bcFileResult) candidateScore {
	var s candidateScore
	for _, r := range results {
		goDefinite := r.Go.Verdict == "SAT" || r.Go.Verdict == "UNSAT"
		rustDefinite := r.Rust.Verdict == "SAT" || r.Rust.Verdict == "UNSAT"
		if !goDefinite {
			s.Unsolved++
		} else {
			s.SolvedGo++
			s.TotalGoSecs += r.Go.ElapsedSeconds
		}
		if !rustDefinite {
			s.Unsolved++
		} else {
			s.SolvedRust++
			s.TotalRustSecs += r.Rust.ElapsedSeconds
		}
		if r.Go.SolutionErr != "" {
			s.Issues++
		}
		if r.Rust.SolutionErr != "" {
			s.Issues++
		}
		if goDefinite && rustDefinite && r.Go.Verdict != r.Rust.Verdict {
			s.Issues++
		}
	}
	s.TotalElapsed = s.TotalGoSecs + s.TotalRustSecs
	return s
}

// less reports whether a is a strictly better candidate than b:
// fewer unsolved files first, then fewer correctness issues, then
// lower total elapsed time among the solved runs.
func (a candidateScore) less(b candidateScore) bool {
	if a.Unsolved != b.Unsolved {
		return a.Unsolved < b.Unsolved
	}
	if a.Issues != b.Issues {
		return a.Issues < b.Issues
	}
	return a.TotalElapsed < b.TotalElapsed
}
