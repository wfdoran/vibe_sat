package main

import "testing"

func solved(elapsed float64) bcRunOutcome {
	return bcRunOutcome{Verdict: "SAT", ElapsedSeconds: elapsed, Solved: true}
}

func TestScoreOfAllSolvedPicksLowerElapsed(t *testing.T) {
	fast := scoreOf([]bcFileResult{{File: "a.cnf", Go: solved(1.0), Rust: solved(1.0)}})
	slow := scoreOf([]bcFileResult{{File: "a.cnf", Go: solved(5.0), Rust: solved(5.0)}})
	if !fast.less(slow) {
		t.Errorf("expected fast (%+v) to be less than slow (%+v)", fast, slow)
	}
	if slow.less(fast) {
		t.Errorf("expected slow (%+v) not to be less than fast (%+v)", slow, fast)
	}
}

func TestScoreOfUnsolvedBeatsElapsedTime(t *testing.T) {
	// A candidate that fails to solve a file is worse than one that
	// solves everything, no matter how slow the solving one is --
	// correctness/completeness must never lose to raw speed.
	unsolvedButFast := scoreOf([]bcFileResult{
		{File: "a.cnf", Go: bcRunOutcome{Verdict: "UNKNOWN"}, Rust: solved(0.001)},
	})
	solvedButSlow := scoreOf([]bcFileResult{
		{File: "a.cnf", Go: solved(1000.0), Rust: solved(1000.0)},
	})
	if !solvedButSlow.less(unsolvedButFast) {
		t.Errorf("expected the slow-but-complete candidate (%+v) to beat the fast-but-incomplete one (%+v)",
			solvedButSlow, unsolvedButFast)
	}
}

func TestScoreOfDetectsMismatchAsIssue(t *testing.T) {
	s := scoreOf([]bcFileResult{
		{File: "a.cnf", Go: bcRunOutcome{Verdict: "SAT"}, Rust: bcRunOutcome{Verdict: "UNSAT"}},
	})
	if s.Issues == 0 {
		t.Errorf("expected a cross-language verdict mismatch to count as an issue, got %+v", s)
	}
}

func TestScoreOfDetectsVerificationFailureAsIssue(t *testing.T) {
	s := scoreOf([]bcFileResult{
		{File: "a.cnf", Go: bcRunOutcome{Verdict: "SAT", SolutionErr: "does not satisfy clause 3"}, Rust: solved(1.0)},
	})
	if s.Issues == 0 {
		t.Errorf("expected a failed solution verification to count as an issue, got %+v", s)
	}
}

func TestScoreOfIssuesBeatsRawSpeedTooButNotUnsolved(t *testing.T) {
	// Issues (correctness red flags among otherwise-complete runs)
	// rank strictly between "some file unsolved" and "just slower":
	// worse than a fully clean, complete, merely-slower candidate,
	// but still better than a candidate that failed to finish a file
	// at all.
	unsolved := scoreOf([]bcFileResult{
		{File: "a.cnf", Go: bcRunOutcome{Verdict: "UNKNOWN"}, Rust: solved(0.001)},
	})
	issueButComplete := scoreOf([]bcFileResult{
		{File: "a.cnf", Go: bcRunOutcome{Verdict: "SAT", SolutionErr: "bad"}, Rust: solved(1.0)},
	})
	cleanButSlow := scoreOf([]bcFileResult{
		{File: "a.cnf", Go: solved(1000.0), Rust: solved(1000.0)},
	})
	if !issueButComplete.less(unsolved) {
		t.Errorf("expected issue-but-complete (%+v) to beat unsolved (%+v)", issueButComplete, unsolved)
	}
	if !cleanButSlow.less(issueButComplete) {
		t.Errorf("expected clean-but-slow (%+v) to beat issue-but-complete (%+v)", cleanButSlow, issueButComplete)
	}
}
