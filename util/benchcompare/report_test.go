package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestMeanAndMedian(t *testing.T) {
	if got := mean(nil); got != 0 {
		t.Errorf("mean(nil) = %v, want 0", got)
	}
	if got := median(nil); got != 0 {
		t.Errorf("median(nil) = %v, want 0", got)
	}
	if got := mean([]float64{1, 2, 3}); got != 2 {
		t.Errorf("mean = %v, want 2", got)
	}
	if got := median([]float64{3, 1, 2}); got != 2 {
		t.Errorf("median (odd) = %v, want 2", got)
	}
	if got := median([]float64{4, 1, 2, 3}); got != 2.5 {
		t.Errorf("median (even) = %v, want 2.5", got)
	}
}

// TestSummarizeCountsAndMismatches builds a small synthetic sweep
// covering every case summarize/languageStatsOf must handle: agreement,
// a verdict mismatch, a verification failure, and a crash.
func TestSummarizeCountsAndMismatches(t *testing.T) {
	results := []fileResult{
		{File: "agree-sat.cnf", Go: runOutcome{Verdict: verdictSAT, Solved: true}, Rust: runOutcome{Verdict: verdictSAT, Solved: true}},
		{File: "agree-unsat.cnf", Go: runOutcome{Verdict: verdictUNSAT}, Rust: runOutcome{Verdict: verdictUNSAT}},
		{File: "mismatch.cnf", Go: runOutcome{Verdict: verdictSAT, Solved: true}, Rust: runOutcome{Verdict: verdictUNSAT}},
		{File: "unknown-not-a-mismatch.cnf", Go: runOutcome{Verdict: verdictUNKNOWN}, Rust: runOutcome{Verdict: verdictSAT, Solved: true}},
		{File: "bad-solution.cnf", Go: runOutcome{Verdict: verdictSAT, Solved: false, SolutionErr: "clause 0 not satisfied"}, Rust: runOutcome{Verdict: verdictSAT, Solved: true}},
		{File: "crash.cnf", Go: runOutcome{Verdict: verdictNone, ExitErr: "exit status 2"}, Rust: runOutcome{Verdict: verdictSAT, Solved: true}},
	}
	s := summarize(results)

	if s.TotalFiles != 6 {
		t.Errorf("TotalFiles = %d, want 6", s.TotalFiles)
	}
	if len(s.Mismatches) != 1 || s.Mismatches[0] != "mismatch.cnf" {
		t.Errorf("Mismatches = %v, want [mismatch.cnf]", s.Mismatches)
	}
	// go: 4 definite verdicts (SAT/SAT/SAT/UNSAT), one of which (SAT,
	// mismatch.cnf) is genuinely solved, one crash, one UNKNOWN, and
	// one solved-but-failed-verification.
	if s.Go.Solved != 4 {
		t.Errorf("Go.Solved = %d, want 4", s.Go.Solved)
	}
	if s.Go.VerificationFails != 1 {
		t.Errorf("Go.VerificationFails = %d, want 1", s.Go.VerificationFails)
	}
	if s.Go.NoVerdict != 1 {
		t.Errorf("Go.NoVerdict = %d, want 1", s.Go.NoVerdict)
	}
	// rust: every one of the 6 files reaches a definite verdict (4 SAT,
	// 2 UNSAT) -- Rust is the "control" side of this synthetic sweep,
	// with no crash/UNKNOWN/verification-failure cases of its own.
	if s.Rust.Solved != 6 {
		t.Errorf("Rust.Solved = %d, want 6", s.Rust.Solved)
	}
}

// TestPrintReportFlagsProblems confirms printReport's output actually
// surfaces the two cases a human sweeping hundreds of files needs to
// notice without reading every line: a cross-language mismatch, and a
// verification failure.
func TestPrintReportFlagsProblems(t *testing.T) {
	results := []fileResult{
		{File: "bad.cnf", Go: runOutcome{Verdict: verdictSAT, SolutionErr: "clause 3 not satisfied"}, Rust: runOutcome{Verdict: verdictUNSAT}},
	}
	s := summarize(results)
	var buf bytes.Buffer
	printReport(&buf, "cdcl", 5*time.Second, results, s)
	out := buf.String()

	if !strings.Contains(out, "MISMATCHES") {
		t.Errorf("report missing MISMATCHES section:\n%s", out)
	}
	if !strings.Contains(out, "bad.cnf") {
		t.Errorf("report missing the flagged file name:\n%s", out)
	}
	if !strings.Contains(out, "clause 3 not satisfied") {
		t.Errorf("report missing the verification failure reason:\n%s", out)
	}
}

func TestPrintReportCleanSweep(t *testing.T) {
	results := []fileResult{
		{File: "ok.cnf", Go: runOutcome{Verdict: verdictSAT, Solved: true}, Rust: runOutcome{Verdict: verdictSAT, Solved: true}},
	}
	s := summarize(results)
	var buf bytes.Buffer
	printReport(&buf, "dfs", time.Second, results, s)
	out := buf.String()
	if !strings.Contains(out, "0 mismatches") {
		t.Errorf("report should report 0 mismatches on a clean sweep:\n%s", out)
	}
	if strings.Contains(out, "flagged runs") {
		t.Errorf("report should have no flagged-runs section on a clean sweep:\n%s", out)
	}
}
