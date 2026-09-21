package main

// Parsing --sweep's mini-language into an ordered list of sweep
// stages, each stage being one or more parameters to vary together.
//
// Grammar: stages are separated by ";" and run in order, coordinate-
// descent style -- each stage's winning combination is fixed before
// the next stage runs (see docs/internal-parameters.md's design notes
// for why coordinate descent, not a single joint sweep, is this
// tool's default). Within one stage, "path=v1|v2|v3" assigns a
// pipe-separated list of candidate values to one parameter; several
// "path=values" assignments joined by "," within the same stage are
// swept jointly, as the full cartesian product of their candidate
// lists -- this is the escape hatch for the rare pair of parameters
// suspected to interact, kept explicit and opt-in rather than the
// default.
//
// Example: "cdcl.glucoseK=0.5|0.6|0.7;preprocess.bveWorkBudgetFactor=1000|2000|4000"
// sweeps glucoseK first (3 candidates, holding everything else at its
// default), fixes the winner, then sweeps bveWorkBudgetFactor (3 more
// candidates, with glucoseK now fixed at its stage-1 winner).
//
// Example: "cdcl.glucoseK=0.5|0.6,cdcl.lrbAlpha=0.3|0.4"
// is a single stage, a 2x2 joint grid of 4 combinations.
import (
	"fmt"
	"strings"
)

// assignment is one "path=value" pairing within a candidate
// combination.
type assignment struct {
	path  string
	value string
}

// combination is one full set of assignments to try together -- one
// benchcompare invocation.
type combination []assignment

// stage is one ";"-separated segment of --sweep: a list of parameter
// paths and each one's candidate values, expanded into every
// combination (cartesian product) that segment asks for.
type stage struct {
	paths        []string
	combinations []combination
}

// parseSweepSpec parses --sweep's full mini-language into an ordered
// list of stages.
func parseSweepSpec(spec string) ([]stage, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("--sweep must not be empty")
	}
	var stages []stage
	for _, stagePart := range strings.Split(spec, ";") {
		stagePart = strings.TrimSpace(stagePart)
		if stagePart == "" {
			continue
		}
		st, err := parseStage(stagePart)
		if err != nil {
			return nil, err
		}
		stages = append(stages, st)
	}
	if len(stages) == 0 {
		return nil, fmt.Errorf("--sweep %q contains no stages", spec)
	}
	return stages, nil
}

func parseStage(s string) (stage, error) {
	var paths []string
	var valueLists [][]string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.Index(part, "=")
		if eq < 0 {
			return stage{}, fmt.Errorf("invalid --sweep assignment %q: expected \"path=v1|v2|...\"", part)
		}
		path := strings.TrimSpace(part[:eq])
		valuesPart := strings.TrimSpace(part[eq+1:])
		if path == "" || valuesPart == "" {
			return stage{}, fmt.Errorf("invalid --sweep assignment %q: expected \"path=v1|v2|...\"", part)
		}
		values := strings.Split(valuesPart, "|")
		for i := range values {
			values[i] = strings.TrimSpace(values[i])
		}
		paths = append(paths, path)
		valueLists = append(valueLists, values)
	}
	if len(paths) == 0 {
		return stage{}, fmt.Errorf("--sweep stage %q has no parameter assignments", s)
	}
	return stage{paths: paths, combinations: cartesianProduct(paths, valueLists)}, nil
}

// cartesianProduct expands paths/valueLists (parallel slices -- one
// candidate-value list per path) into every combination of one value
// per path.
func cartesianProduct(paths []string, valueLists [][]string) []combination {
	combos := []combination{{}}
	for i, path := range paths {
		var next []combination
		for _, c := range combos {
			for _, v := range valueLists[i] {
				extended := make(combination, len(c), len(c)+1)
				copy(extended, c)
				extended = append(extended, assignment{path: path, value: v})
				next = append(next, extended)
			}
		}
		combos = next
	}
	return combos
}

// describe renders a combination as "path1=v1, path2=v2" for reports.
func (c combination) describe() string {
	parts := make([]string, len(c))
	for i, a := range c {
		parts[i] = a.path + "=" + a.value
	}
	return strings.Join(parts, ", ")
}
