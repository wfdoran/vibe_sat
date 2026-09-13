// Package cliargs parses the command line arguments shared by the
// vibe_sat program. Every argument supports both a long form
// (--long-name=value or --long-name value) and a short form
// (-x value); both forms write into the same field so either
// spelling may be used interchangeably.
//
// The standard library's flag package is intentionally not used here:
// --alg-params/-p can take between one and three values
// (--alg-params <val1> [<val2> <val3>]), which flag's one-value-per-
// flag model cannot express, so this package implements its own small
// tokenizer instead.
package cliargs

import (
	"fmt"
	"strconv"
	"strings"
)

// Args holds the parsed command line arguments for vibe_sat.
type Args struct {
	Help          bool    // --help / -h : print usage information and exit
	InputFile     string  // --input / -i : path to the DIMACS CNF file to read (required)
	Verbose       int     // --verbose / -v : verbosity level, default 0
	Algorithm     string  // --algorithm / -a : solving algorithm to use (required; only "hc" is supported)
	OutputFile    string  // --output / -o : where to write the solution, if any ("" means unset)
	TimeLimitSecs *int    // --time-limit-secs / -t : optional search time limit, in seconds
	AlgParams     []int64 // --alg-params / -p : 1 to 3 algorithm-specific integer parameters
}

// flagSpec describes one recognized flag: its long and short
// spellings, and the maximum number of value tokens it consumes when
// given in the space-separated form (e.g. "-p 10 20"). Every flag
// except --alg-params/-p consumes exactly one value.
type flagSpec struct {
	long      string
	short     string
	maxValues int
}

// flagSpecs lists every flag vibe_sat currently understands.
var flagSpecs = []flagSpec{
	{long: "input", short: "i", maxValues: 1},
	{long: "verbose", short: "v", maxValues: 1},
	{long: "algorithm", short: "a", maxValues: 1},
	{long: "output", short: "o", maxValues: 1},
	{long: "time-limit-secs", short: "t", maxValues: 1},
	{long: "alg-params", short: "p", maxValues: 3},
}

// findSpec returns the flagSpec whose long or short spelling matches
// name exactly, matched against the long spelling only for "--"
// tokens and the short spelling only for "-" tokens. ok is false if
// no flag matches.
func findSpec(name string, isLongForm bool) (spec flagSpec, ok bool) {
	for _, s := range flagSpecs {
		if isLongForm && s.long == name {
			return s, true
		}
		if !isLongForm && s.short == name {
			return s, true
		}
	}
	return flagSpec{}, false
}

// splitFlagToken parses a single command line token such as
// "--input=problem.cnf", "--input", or "-i" into the flag name it
// names and, if the token included an inline "=value" suffix, that
// value. isFlag is false if token does not look like a flag at all
// (i.e. does not start with '-'), in which case the other return
// values are meaningless.
func splitFlagToken(token string) (name string, inlineValue string, hasInline bool, isLongForm bool, isFlag bool) {
	switch {
	case strings.HasPrefix(token, "--"):
		isFlag, isLongForm = true, true
		rest := token[2:]
		if idx := strings.IndexByte(rest, '='); idx >= 0 {
			return rest[:idx], rest[idx+1:], true, true, true
		}
		return rest, "", false, true, true

	case strings.HasPrefix(token, "-") && len(token) > 1 && !looksLikeNegativeNumber(token):
		isFlag, isLongForm = true, false
		rest := token[1:]
		if idx := strings.IndexByte(rest, '='); idx >= 0 {
			return rest[:idx], rest[idx+1:], true, false, true
		}
		return rest, "", false, false, true

	default:
		return "", "", false, false, false
	}
}

// looksLikeNegativeNumber reports whether token is "-" immediately
// followed by a digit, e.g. "-5". Such tokens are treated as value
// tokens rather than flags, so that a negative numeric value passed
// to --alg-params is not mistaken for the start of a new flag.
func looksLikeNegativeNumber(token string) bool {
	return len(token) > 1 && token[0] == '-' && token[1] >= '0' && token[1] <= '9'
}

// Parse parses argv (typically os.Args[1:], i.e. excluding the
// program name) into an Args structure. It returns an error if argv
// cannot be parsed, if a required argument is missing, or if the
// arguments given are inconsistent with each other (for example,
// neither --time-limit-secs nor --alg-params given for --algorithm=hc).
//
// --help/-h is special-cased ahead of everything else: if either is
// present anywhere in argv, Parse immediately returns an Args with
// only Help set to true, skipping every other token (including ones
// that would otherwise be errors, such as a missing --input). This
// matches the usual expectation that --help always works, even when
// the rest of the command line is incomplete or wrong.
func Parse(argv []string) (*Args, error) {
	for _, token := range argv {
		if token == "--help" || token == "-h" {
			return &Args{Help: true}, nil
		}
	}

	rawValues, err := tokenize(argv)
	if err != nil {
		return nil, err
	}

	args, err := buildArgs(rawValues)
	if err != nil {
		return nil, err
	}

	if err := validate(args, rawValues); err != nil {
		return nil, err
	}
	return args, nil
}

// tokenize scans argv and groups each flag's value tokens under its
// canonical long name, without interpreting or validating any of the
// values. This is the part of parsing that is independent of which
// algorithm was selected, which is why it is kept separate from
// buildArgs and validate below.
func tokenize(argv []string) (map[string][]string, error) {
	rawValues := map[string][]string{}

	i := 0
	for i < len(argv) {
		token := argv[i]
		name, inlineValue, hasInline, isLongForm, isFlag := splitFlagToken(token)
		if !isFlag {
			return nil, fmt.Errorf("unexpected argument %q", token)
		}
		spec, ok := findSpec(name, isLongForm)
		if !ok {
			return nil, fmt.Errorf("unrecognized flag %q", token)
		}

		var collected []string
		if hasInline {
			collected = []string{inlineValue}
			i++
		} else {
			i++
			for len(collected) < spec.maxValues && i < len(argv) {
				_, _, _, _, nextIsFlag := splitFlagToken(argv[i])
				if nextIsFlag {
					break
				}
				collected = append(collected, argv[i])
				i++
			}
			if len(collected) == 0 {
				return nil, fmt.Errorf("flag %q requires a value", token)
			}
		}
		rawValues[spec.long] = collected
	}

	return rawValues, nil
}

// buildArgs converts the raw flag value tokens collected by tokenize
// into a typed Args structure, reporting an error if any value cannot
// be parsed as the type it is expected to have.
func buildArgs(rawValues map[string][]string) (*Args, error) {
	args := &Args{}

	if values, ok := rawValues["input"]; ok {
		args.InputFile = values[0]
	}
	if values, ok := rawValues["verbose"]; ok {
		v, err := strconv.Atoi(values[0])
		if err != nil {
			return nil, fmt.Errorf("invalid value for --verbose: %q", values[0])
		}
		args.Verbose = v
	}
	if values, ok := rawValues["algorithm"]; ok {
		args.Algorithm = values[0]
	}
	if values, ok := rawValues["output"]; ok {
		args.OutputFile = values[0]
	}
	if values, ok := rawValues["time-limit-secs"]; ok {
		t, err := strconv.Atoi(values[0])
		if err != nil {
			return nil, fmt.Errorf("invalid value for --time-limit-secs: %q", values[0])
		}
		args.TimeLimitSecs = &t
	}
	if values, ok := rawValues["alg-params"]; ok {
		for _, value := range values {
			p, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid value for --alg-params: %q", value)
			}
			args.AlgParams = append(args.AlgParams, p)
		}
	}

	return args, nil
}

// validate checks that the parsed Args are complete and internally
// consistent: that every required argument was supplied, and that the
// combination of algorithm-specific arguments makes sense for the
// chosen algorithm.
func validate(args *Args, rawValues map[string][]string) error {
	if args.InputFile == "" {
		return fmt.Errorf("missing required argument: --input=<filename> (or -i <filename>)")
	}
	if _, ok := rawValues["algorithm"]; !ok {
		return fmt.Errorf("missing required argument: --algorithm=<string> (or -a <string>)")
	}

	switch args.Algorithm {
	case "hc":
		if len(args.AlgParams) > 1 {
			return fmt.Errorf("for --algorithm=hc, --alg-params accepts at most one value (the number of starts)")
		}
		if args.TimeLimitSecs == nil && len(args.AlgParams) == 0 {
			return fmt.Errorf("for --algorithm=hc, either --time-limit-secs or --alg-params (number of starts) must be given")
		}
		if len(args.AlgParams) == 1 && args.AlgParams[0] < 1 {
			return fmt.Errorf("for --algorithm=hc, the number of starts given via --alg-params must be a positive integer")
		}
		if args.TimeLimitSecs != nil && *args.TimeLimitSecs < 1 {
			return fmt.Errorf("--time-limit-secs must be a positive integer")
		}

	case "ws":
		if args.TimeLimitSecs == nil && len(args.AlgParams) == 0 {
			return fmt.Errorf("for --algorithm=ws, either --time-limit-secs or --alg-params (number of tries) must be given")
		}
		if len(args.AlgParams) >= 1 && args.AlgParams[0] < 1 {
			return fmt.Errorf("for --algorithm=ws, the number of tries given via --alg-params must be a positive integer")
		}
		if len(args.AlgParams) >= 2 && args.AlgParams[1] < 1 {
			return fmt.Errorf("for --algorithm=ws, the max-flips-per-try value given via --alg-params must be a positive integer")
		}
		if len(args.AlgParams) >= 3 && (args.AlgParams[2] < 0 || args.AlgParams[2] > 100) {
			return fmt.Errorf("for --algorithm=ws, the noise-percent value given via --alg-params must be between 0 and 100")
		}
		if args.TimeLimitSecs != nil && *args.TimeLimitSecs < 1 {
			return fmt.Errorf("--time-limit-secs must be a positive integer")
		}

	case "dfs":
		if len(args.AlgParams) > 1 {
			return fmt.Errorf("for --algorithm=dfs, --alg-params accepts at most one value (0 or 1, selecting which SelectVar heuristic to use)")
		}
		if len(args.AlgParams) == 1 && args.AlgParams[0] != 0 && args.AlgParams[0] != 1 {
			return fmt.Errorf("for --algorithm=dfs, the --alg-params value must be 0 or 1 (selecting which SelectVar heuristic to use)")
		}
		if args.TimeLimitSecs != nil && *args.TimeLimitSecs < 1 {
			return fmt.Errorf("--time-limit-secs must be a positive integer")
		}

	default:
		return fmt.Errorf("unsupported --algorithm value %q; only \"hc\", \"ws\", and \"dfs\" are currently supported", args.Algorithm)
	}

	return nil
}
