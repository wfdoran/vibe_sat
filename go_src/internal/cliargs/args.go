// Package cliargs parses the command line arguments shared by the
// vibe_sat program. Every argument supports both a long form
// (--long-name=value or --long-name value) and a short form
// (-x value); both forms write into the same field so either
// spelling may be used interchangeably.
//
// The standard library's flag package is intentionally not used here:
// --alg-params/-p can take between one and four values
// (--alg-params <val1> [<val2> <val3> <val4>]), which flag's
// one-value-per-flag model cannot express, so this package implements
// its own small tokenizer instead.
package cliargs

import (
	"fmt"
	"strconv"
	"strings"
)

// Args holds the parsed command line arguments for vibe_sat.
type Args struct {
	Help          bool   // --help / -h : print usage information and exit
	InputFile     string // --input / -i : path to the DIMACS CNF file to read (required, unless ResetInternalParams)
	Verbose       int    // --verbose / -v : verbosity level, default 0
	Algorithm     string // --algorithm / -a : solving algorithm to use (required, unless ResetInternalParams; "hc", "ws", "dfs", or "cdcl")
	OutputFile    string // --output / -o : where to write the solution, if any ("" means unset)
	TimeLimitSecs *int   // --time-limit-secs / -t : optional search time limit, in seconds

	// AlgParams is --alg-params / -p : 1 to 4 algorithm-specific
	// integer parameters. Each element is nil if and only if that
	// position was given as "_" (STAGE44.md's underscore syntax,
	// meaning "use this slot's own default" without needing to know or
	// spell out what that default actually is) -- distinct from a
	// position simply not being present at all (a shorter slice), but
	// every consumer treats the two identically: "fall through to
	// whatever this slot's own default logic already does." This is
	// what lets a caller reach a later slot (e.g. cdcl's val4) without
	// committing to a real value for an earlier one they don't care
	// about (e.g. "--alg-params _ _ _ 3" sets only the phase strategy).
	AlgParams       []*int64
	NoPreprocessing bool // --no-preprocessing / -x : skip preprocessing (STAGE8.md); default is to run it

	// InternalParamsPath is --internal-params/-c (STAGE39.md): an
	// explicit path to a JSON file of runtime-configurable internal
	// tuning constants (see internal/params), overriding the implicit
	// lookup for params.DefaultConfigFileName in the current directory.
	// "" means unset -- not "use no config file at all," since the
	// implicit lookup still applies; see params.Resolve.
	InternalParamsPath string

	// ResetInternalParams is --reset-internal-params/-q (STAGE39.md):
	// write the resolved config path (InternalParamsPath if given,
	// else params.DefaultConfigFileName) with every internal
	// parameter's built-in default value, then exit without reading
	// --input or running anything -- a starting point for a user who
	// wants to override a handful of values without needing to know
	// every field name and its current default ahead of time. Like
	// Help, this makes --input/--algorithm optional (see validate).
	ResetInternalParams bool

	// NumThreads is --num-threads/-z (STAGE17.md): the number of
	// concurrent worker threads to use, currently honored only by
	// --algorithm=hc/ws (see main's runHillClimb/runWalkSat). Defaults
	// to 1 (single-threaded, matching every algorithm's behavior
	// before Stage 17) if not given. Deliberately unbounded above --
	// a value larger than the machine's core count is allowed
	// (oversubscription); main prints a one-line warning at
	// --verbose >= 1 if it looks large enough to be unintentional.
	NumThreads int

	// MemoryLimitBytes is --alg-params/-p's third value for
	// --algorithm=cdcl only (STAGE12.md; shifted from the second value
	// to the third by STAGE15.md, which inserted the restart strategy
	// as the new second value): an optional learned-clause database
	// memory limit, in bytes. Parsed by parseByteSize from either a
	// plain integer (bytes) or an integer immediately followed by
	// "k"/"kb"/"m"/"mb"/"g"/"gb" (case-insensitive; see
	// --alg-params 0 1 100MB in the help text). nil if not given, in
	// which case the database grows without bound, as it did before
	// Stage 12. Kept separate from AlgParams (rather than as its third
	// element) since it isn't a plain integer, unlike every other
	// --alg-params value in this program.
	MemoryLimitBytes *int64
}

// flagSpec describes one recognized flag: its long and short
// spellings, and the maximum number of value tokens it consumes when
// given in the space-separated form (e.g. "-p 10 20"). A maxValues of
// 0 marks a boolean flag that takes no value at all (e.g.
// --no-preprocessing); every other flag here consumes exactly one
// value, except --alg-params/-p, which takes up to four.
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
	{long: "alg-params", short: "p", maxValues: 4},
	{long: "no-preprocessing", short: "x", maxValues: 0},
	{long: "num-threads", short: "z", maxValues: 1},
	{long: "internal-params", short: "c", maxValues: 1},
	{long: "reset-internal-params", short: "q", maxValues: 0},
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

		if spec.maxValues == 0 {
			if hasInline {
				return nil, fmt.Errorf("flag %q does not take a value", token)
			}
			i++
			rawValues[spec.long] = []string{}
			continue
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

// parseAlgParamValue parses one plain-integer --alg-params value
// token: "_" (STAGE44.md) returns (nil, nil) -- "use this slot's own
// default," represented as a nil pointer rather than any particular
// sentinel integer, since every slot's actual default value differs
// (and, for cdcl's restart/phase strategies, depends on --num-threads,
// which buildArgs can't assume has already been parsed at this point
// anyway) -- so the real default is resolved later, by whichever
// consumer already resolves "not given at all." Anything else must be
// a base-10 integer.
func parseAlgParamValue(s string) (*int64, error) {
	if s == "_" {
		return nil, nil
	}
	p, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid value for --alg-params: %q", s)
	}
	return &p, nil
}

// parseByteSize parses s as a byte count (STAGE12.md): either a plain
// non-negative integer (a number of bytes), or such an integer
// immediately followed by one of "k", "kb", "m", "mb", "g", or "gb"
// (case-insensitive; e.g. "100MB", "100mb", and "100Mb" all parse the
// same way), for kilobytes, megabytes, or gigabytes (each 1024 times
// the previous unit, not 1000).
func parseByteSize(s string) (int64, error) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("%q does not start with a number", s)
	}

	n, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid byte size: %w", s, err)
	}

	var multiplier int64
	switch strings.ToLower(s[i:]) {
	case "":
		multiplier = 1
	case "k", "kb":
		multiplier = 1024
	case "m", "mb":
		multiplier = 1024 * 1024
	case "g", "gb":
		multiplier = 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("%q has an unrecognized unit %q; expected one of k, kb, m, mb, g, gb", s, s[i:])
	}

	return n * multiplier, nil
}

// buildArgs converts the raw flag value tokens collected by tokenize
// into a typed Args structure, reporting an error if any value cannot
// be parsed as the type it is expected to have.
func buildArgs(rawValues map[string][]string) (*Args, error) {
	args := &Args{NumThreads: 1}

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
		// --algorithm=cdcl's third --alg-params value is a memory size
		// (STAGE12.md), not a plain integer like every other
		// --alg-params value in this program, so it needs its own
		// parsing path rather than the uniform loop below; this is why
		// buildArgs (usually algorithm-agnostic) branches on
		// args.Algorithm here. Per STAGE15.md, the second value
		// (restart strategy) is a plain integer, appended to AlgParams
		// just like the first.
		if args.Algorithm == "cdcl" {
			if len(values) > 4 {
				return nil, fmt.Errorf("for --algorithm=cdcl, --alg-params accepts at most four values (0-3 selecting which SelectVar heuristic to use, 0-5 selecting the restart strategy, an optional learned-clause database memory limit, and 0-3 selecting the phase-selection strategy)")
			}
			if len(values) >= 1 {
				p, err := parseAlgParamValue(values[0])
				if err != nil {
					return nil, err
				}
				args.AlgParams = append(args.AlgParams, p)
			}
			if len(values) >= 2 {
				p, err := parseAlgParamValue(values[1])
				if err != nil {
					return nil, err
				}
				args.AlgParams = append(args.AlgParams, p)
			}
			if len(values) >= 3 {
				// "_" (STAGE44.md) leaves MemoryLimitBytes nil, exactly
				// as if val3 had never been given at all -- unbounded,
				// the same default every other unset slot falls back to.
				if values[2] != "_" {
					limit, err := parseByteSize(values[2])
					if err != nil {
						return nil, fmt.Errorf("invalid memory limit for --alg-params: %w", err)
					}
					args.MemoryLimitBytes = &limit
				}
			}
			if len(values) == 4 {
				// STAGE43.md's phase-selection strategy: a plain integer,
				// like val1/val2, appended to AlgParams as its third
				// element (AlgParams[2]) even though it's the *fourth*
				// raw --alg-params token -- MemoryLimitBytes (the third
				// token) is parsed into its own field above, not into
				// AlgParams, so AlgParams itself only ever holds
				// SelectVar/restart/phase, never the memory limit.
				// STAGE44.md's underscore syntax is what actually fixes
				// the old wrinkle noted here (selecting a phase strategy
				// used to require also giving a *real* memory limit as
				// val3): "--alg-params _ _ _ 3" now reaches val4 while
				// leaving val1/val2/val3 all at their own defaults.
				p, err := parseAlgParamValue(values[3])
				if err != nil {
					return nil, err
				}
				args.AlgParams = append(args.AlgParams, p)
			}
		} else {
			for _, value := range values {
				p, err := parseAlgParamValue(value)
				if err != nil {
					return nil, err
				}
				args.AlgParams = append(args.AlgParams, p)
			}
		}
	}
	if _, ok := rawValues["no-preprocessing"]; ok {
		args.NoPreprocessing = true
	}
	if values, ok := rawValues["num-threads"]; ok {
		n, err := strconv.Atoi(values[0])
		if err != nil {
			return nil, fmt.Errorf("invalid value for --num-threads: %q", values[0])
		}
		args.NumThreads = n
	}
	if values, ok := rawValues["internal-params"]; ok {
		args.InternalParamsPath = values[0]
	}
	if _, ok := rawValues["reset-internal-params"]; ok {
		args.ResetInternalParams = true
	}

	return args, nil
}

// validate checks that the parsed Args are complete and internally
// consistent: that every required argument was supplied, and that the
// combination of algorithm-specific arguments makes sense for the
// chosen algorithm.
func validate(args *Args, rawValues map[string][]string) error {
	// --reset-internal-params writes a config file and exits without
	// ever reading --input or running an algorithm (see main.go), so
	// neither is required in that case -- the same carve-out --help
	// already gets, just via validate rather than Parse's early-return
	// loop, since --reset-internal-params still needs the normal
	// tokenizer to run first (to pick up --internal-params, if given).
	if args.ResetInternalParams {
		return nil
	}
	if args.InputFile == "" {
		return fmt.Errorf("missing required argument: --input=<filename> (or -i <filename>)")
	}
	if _, ok := rawValues["algorithm"]; !ok {
		return fmt.Errorf("missing required argument: --algorithm=<string> (or -a <string>)")
	}
	if args.NumThreads < 1 {
		return fmt.Errorf("--num-threads must be a positive integer")
	}

	switch args.Algorithm {
	case "hc":
		if len(args.AlgParams) > 1 {
			return fmt.Errorf("for --algorithm=hc, --alg-params accepts at most one value (the number of starts)")
		}
		if args.TimeLimitSecs == nil && len(args.AlgParams) == 0 {
			return fmt.Errorf("for --algorithm=hc, either --time-limit-secs or --alg-params (number of starts) must be given")
		}
		if len(args.AlgParams) == 1 && args.AlgParams[0] != nil && *args.AlgParams[0] < 1 {
			return fmt.Errorf("for --algorithm=hc, the number of starts given via --alg-params must be a positive integer")
		}
		if args.TimeLimitSecs != nil && *args.TimeLimitSecs < 1 {
			return fmt.Errorf("--time-limit-secs must be a positive integer")
		}

	case "ws":
		if args.TimeLimitSecs == nil && len(args.AlgParams) == 0 {
			return fmt.Errorf("for --algorithm=ws, either --time-limit-secs or --alg-params (number of tries) must be given")
		}
		if len(args.AlgParams) >= 1 && args.AlgParams[0] != nil && *args.AlgParams[0] < 1 {
			return fmt.Errorf("for --algorithm=ws, the number of tries given via --alg-params must be a positive integer")
		}
		if len(args.AlgParams) >= 2 && args.AlgParams[1] != nil && *args.AlgParams[1] < 1 {
			return fmt.Errorf("for --algorithm=ws, the max-flips-per-try value given via --alg-params must be a positive integer")
		}
		if len(args.AlgParams) >= 3 && args.AlgParams[2] != nil && (*args.AlgParams[2] < 0 || *args.AlgParams[2] > 100) {
			return fmt.Errorf("for --algorithm=ws, the noise-percent value given via --alg-params must be between 0 and 100")
		}
		if args.TimeLimitSecs != nil && *args.TimeLimitSecs < 1 {
			return fmt.Errorf("--time-limit-secs must be a positive integer")
		}

	case "dfs":
		if len(args.AlgParams) > 1 {
			return fmt.Errorf("for --algorithm=dfs, --alg-params accepts at most one value (0 or 1, selecting which SelectVar heuristic to use)")
		}
		if len(args.AlgParams) == 1 && args.AlgParams[0] != nil && *args.AlgParams[0] != 0 && *args.AlgParams[0] != 1 {
			return fmt.Errorf("for --algorithm=dfs, the --alg-params value must be 0 or 1 (selecting which SelectVar heuristic to use)")
		}
		if args.TimeLimitSecs != nil && *args.TimeLimitSecs < 1 {
			return fmt.Errorf("--time-limit-secs must be a positive integer")
		}

	case "cdcl":
		// The at-most-four-values check and the memory limit's own
		// syntax (plain integer, optionally with a k/kb/m/mb/g/gb
		// suffix) were already enforced in buildArgs, since that's
		// where the raw tokens are available; only the remaining
		// business rules (variant is 0-3; restart strategy is 0-5;
		// the limit, if given, is positive; phase strategy is 0-3) are
		// checked here -- each skipped (nil, "_") if the caller used
		// STAGE44.md's underscore syntax for that slot, since there's
		// nothing to range-check about "use the default."
		// STAGE13.md extends the first value's range from dfs's 0/1
		// (Weighted/Fast) to also allow 2 (VSIDS) and 3 (LRB), both
		// cdcl-only. STAGE15.md adds the second value (restart
		// strategy: 0 = none, 1 = Luby, 2 = polynomial, 3 = geometric).
		// STAGE21.md adds a fourth restart-strategy value (round-robin
		// across the fixed-schedule strategies by worker index, see
		// cdcl.RestartRoundRobin) and STAGE34.md a fifth (Glucose's own
		// data-driven policy based on LBD, see cdcl.RestartGlucose);
		// STAGE44.md swaps which numeral is which (4 = Glucose, 5 =
		// round-robin, now spanning all four fixed/data-driven
		// strategies) so that round-robin -- the "meta" choice -- keeps
		// the highest number as the strategy list grows, rather than
		// sitting in the middle of it.
		// STAGE43.md adds a fourth AlgParams element (phase strategy:
		// 0 = saving, 1 = target, 2 = WalkSAT rephasing, 3 = round-robin
		// across all three by worker index; see cdcl.PhaseStrategy).
		if len(args.AlgParams) >= 1 && args.AlgParams[0] != nil && (*args.AlgParams[0] < 0 || *args.AlgParams[0] > 3) {
			return fmt.Errorf("for --algorithm=cdcl, the first --alg-params value must be 0, 1, 2, or 3 (selecting which SelectVar heuristic to use)")
		}
		if len(args.AlgParams) >= 2 && args.AlgParams[1] != nil && (*args.AlgParams[1] < 0 || *args.AlgParams[1] > 5) {
			return fmt.Errorf("for --algorithm=cdcl, the second --alg-params value must be 0, 1, 2, 3, 4, or 5 (selecting the restart strategy)")
		}
		if args.MemoryLimitBytes != nil && *args.MemoryLimitBytes < 1 {
			return fmt.Errorf("for --algorithm=cdcl, the memory limit given via --alg-params must be a positive number of bytes")
		}
		if len(args.AlgParams) >= 3 && args.AlgParams[2] != nil && (*args.AlgParams[2] < 0 || *args.AlgParams[2] > 3) {
			return fmt.Errorf("for --algorithm=cdcl, the fourth --alg-params value must be 0, 1, 2, or 3 (selecting the phase-selection strategy)")
		}
		if args.TimeLimitSecs != nil && *args.TimeLimitSecs < 1 {
			return fmt.Errorf("--time-limit-secs must be a positive integer")
		}

	default:
		return fmt.Errorf("unsupported --algorithm value %q; only \"hc\", \"ws\", \"dfs\", and \"cdcl\" are currently supported", args.Algorithm)
	}

	return nil
}
