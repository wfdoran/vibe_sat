package main

// Aggregating and printing the results of a benchcompare sweep: one
// fileResult per benchmark file, summarized into the kind of table
// every stage from REPORT8.md through REPORT23.md has hand-built (and
// then thrown away) a one-off script to produce.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"
	"time"
)

// fileResult pairs one benchmark file with what each binary did with
// it, under identical arguments.
type fileResult struct {
	File string     `json:"file"`
	Go   runOutcome `json:"go"`
	Rust runOutcome `json:"rust"`
}

// languageStats summarizes one binary's behavior across an entire
// sweep: how many runs reached a definite verdict within the harness's
// hard timeout, how long the solved ones took, and whether any
// reported SAT solution failed this harness's own independent check.
type languageStats struct {
	Solved            int // verdict is SAT or UNSAT (a definite answer, not UNKNOWN/NONE)
	SATCount          int
	UNSATCount        int
	VerificationFails int // verdict == SAT but the solution did not check out
	NoVerdict         int // verdict == NONE: either a real crash, or killed by --hard-timeout-secs (see printFlagged for which)
	MeanSolvedSeconds float64
	MedianSolvedSecs  float64
}

// summary is the whole-sweep report: per-language stats plus
// cross-language agreement, which is this project's primary
// correctness signal since REPORT8.md.
type summary struct {
	TotalFiles int
	Go         languageStats
	Rust       languageStats
	Mismatches []string // file names where Go and Rust reached different definite verdicts
}

// summarize computes a summary from a completed sweep's results.
func summarize(results []fileResult) summary {
	var s summary
	s.TotalFiles = len(results)
	s.Go = languageStatsOf(results, func(fr fileResult) runOutcome { return fr.Go })
	s.Rust = languageStatsOf(results, func(fr fileResult) runOutcome { return fr.Rust })

	for _, fr := range results {
		gv, rv := fr.Go.Verdict, fr.Rust.Verdict
		definite := func(v verdict) bool { return v == verdictSAT || v == verdictUNSAT }
		if definite(gv) && definite(rv) && gv != rv {
			s.Mismatches = append(s.Mismatches, fr.File)
		}
	}
	return s
}

// languageStatsOf extracts one language's runOutcome from every
// fileResult (via pick) and reduces them to a languageStats.
func languageStatsOf(results []fileResult, pick func(fileResult) runOutcome) languageStats {
	var stats languageStats
	var solvedTimes []float64
	for _, fr := range results {
		o := pick(fr)
		switch o.Verdict {
		case verdictSAT:
			stats.SATCount++
			stats.Solved++
			solvedTimes = append(solvedTimes, o.ElapsedSeconds)
			if !o.Solved {
				stats.VerificationFails++
			}
		case verdictUNSAT:
			stats.UNSATCount++
			stats.Solved++
			solvedTimes = append(solvedTimes, o.ElapsedSeconds)
		case verdictNone:
			stats.NoVerdict++
		}
	}
	stats.MeanSolvedSeconds = mean(solvedTimes)
	stats.MedianSolvedSecs = median(solvedTimes)
	return stats
}

// mean returns the arithmetic mean of xs, or 0 for an empty slice.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// median returns the median of xs (average of the two middle values
// for an even-length slice), or 0 for an empty slice. xs is sorted in
// place.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	mid := len(xs) / 2
	if len(xs)%2 == 1 {
		return xs[mid]
	}
	return (xs[mid-1] + xs[mid]) / 2
}

// printReport writes a human-readable summary table to w, followed by
// a per-file detail section for anything that looks wrong (a
// mismatch, a verification failure, a crash, or a harness-imposed
// timeout kill) -- the cases worth a human actually looking at, rather
// than every one of possibly hundreds of files.
func printReport(w io.Writer, algorithm string, elapsedWall time.Duration, results []fileResult, s summary) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "benchcompare: --algorithm=%s, %d files, %s wall clock\n\n", algorithm, s.TotalFiles, elapsedWall.Round(time.Millisecond))
	fmt.Fprintln(tw, "language\tsolved\t(SAT/UNSAT)\tverify fails\tno verdict\tmean (solved)\tmedian (solved)")
	printLanguageRow(tw, "go", s.Go)
	printLanguageRow(tw, "rust", s.Rust)
	tw.Flush()

	fmt.Fprintln(w)
	if len(s.Mismatches) == 0 {
		fmt.Fprintln(w, "cross-language verdict agreement: 0 mismatches")
	} else {
		fmt.Fprintf(w, "cross-language verdict MISMATCHES (%d):\n", len(s.Mismatches))
		for _, f := range s.Mismatches {
			fmt.Fprintln(w, "  ", f)
		}
	}

	printFlagged(w, results)
}

// printLanguageRow writes one summary-table row for a language.
func printLanguageRow(tw *tabwriter.Writer, name string, ls languageStats) {
	fmt.Fprintf(tw, "%s\t%d\t(%d/%d)\t%d\t%d\t%.3fs\t%.3fs\n",
		name, ls.Solved, ls.SATCount, ls.UNSATCount, ls.VerificationFails, ls.NoVerdict,
		ls.MeanSolvedSeconds, ls.MedianSolvedSecs)
}

// printFlagged prints one line per file that needs a human's
// attention: a verification failure, a crash (no recognizable verdict
// line), or a process error (non-zero exit/kill not explained by the
// harness's own hard timeout). A clean sweep prints nothing here.
func printFlagged(w io.Writer, results []fileResult) {
	header := false
	note := func(file, lang string, o runOutcome) {
		if !header {
			fmt.Fprintln(w, "\nflagged runs (need a human look):")
			header = true
		}
		reason := "unrecognized verdict output"
		switch {
		case o.SolutionErr != "":
			reason = "solution failed independent verification: " + o.SolutionErr
		case o.TimedOutOS:
			reason = "killed by harness hard-timeout (exceeded --hard-timeout-secs)"
		case o.ExitErr != "":
			reason = "process error: " + o.ExitErr
		}
		fmt.Fprintf(w, "  %s [%s]: %s\n", file, lang, reason)
	}
	for _, fr := range results {
		if fr.Go.SolutionErr != "" || fr.Go.Verdict == verdictNone || (fr.Go.ExitErr != "" && !fr.Go.TimedOutOS) {
			note(fr.File, "go", fr.Go)
		}
		if fr.Rust.SolutionErr != "" || fr.Rust.Verdict == verdictNone || (fr.Rust.ExitErr != "" && !fr.Rust.TimedOutOS) {
			note(fr.File, "rust", fr.Rust)
		}
	}
}

// writeJSON writes the full, unaggregated per-file results to path as
// JSON, for anyone who wants to slice the data differently than
// printReport does.
func writeJSON(path string, results []fileResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(results)
}
