//! In-memory representation of a SAT problem in conjunctive normal
//! form (CNF), along with routines to read such a problem from a
//! DIMACS formatted file and to print summary statistics about it.

use std::fs::File;
use std::io::{self, BufRead, Write};

/// A single DIMACS literal. A positive value asserts that the
/// referenced variable is true; a negative value asserts that the
/// referenced variable is false (negated). The magnitude of the value
/// is the 1-based index of the variable being referenced. The value 0
/// is never stored as a `Literal`; in DIMACS files it is only used as
/// a clause terminator and is consumed during parsing.
pub type Literal = i32;

/// Returns the 1-based variable index referenced by `literal`,
/// independent of its polarity (sign).
pub fn literal_var(literal: Literal) -> usize {
    literal.unsigned_abs() as usize
}

/// Returns whether `literal` is the negation of its underlying
/// variable (i.e. whether the original DIMACS value was negative).
pub fn literal_is_negative(literal: Literal) -> bool {
    literal < 0
}

/// A clause: a disjunction of literals. An empty clause represents the
/// empty clause, which is always false and therefore makes the whole
/// formula unsatisfiable.
pub type Clause = Vec<Literal>;

/// An entire SAT instance in conjunctive normal form: the number of
/// variables declared by the problem, and the list of clauses that
/// must all be satisfied simultaneously. Literals within a clause
/// refer to variables by number, from 1 to `num_vars`.
///
/// This representation intentionally keeps clauses as plain vectors of
/// literals, with no shared mutable state, so that it can later be
/// shared read-only across async tasks or worker threads once the
/// solver runs on multiple cores.
pub struct Problem {
    /// Number of variables declared on the "p cnf" line.
    pub num_vars: usize,
    /// The clauses making up the formula.
    pub clauses: Vec<Clause>,
}

impl Problem {
    /// Returns the number of clauses currently stored in the problem.
    pub fn num_clauses(&self) -> usize {
        self.clauses.len()
    }

    /// Returns the total number of literal occurrences across every
    /// clause in the problem (i.e. the sum of each clause's length,
    /// not the number of distinct literals).
    pub fn num_literals(&self) -> usize {
        self.clauses.iter().map(|clause| clause.len()).sum()
    }
}

/// Reads the file named by `filename` and parses it as a DIMACS
/// formatted CNF file, returning the resulting `Problem`.
///
/// The DIMACS CNF format consists of:
///   - zero or more comment lines beginning with 'c'
///   - exactly one problem line of the form "p cnf <numVars> <numClauses>"
///   - a sequence of clauses, each a whitespace separated list of
///     non-zero signed integers (literals) terminated by a 0; a clause
///     may span multiple lines
///
/// Some benchmark files (notably the SATLIB set) end the clause
/// section with a line containing only '%'; anything from that line
/// onward is ignored.
///
/// If `verbose` is >= 1, the name of the file being read is printed to
/// stdout before it is opened. If any error is encountered (the file
/// cannot be opened, or its contents are not well-formed DIMACS CNF),
/// an `Err` is returned describing the problem; the caller is expected
/// to report this to the user and exit with a non-zero status.
pub fn read_dimacs(filename: &str, verbose: i32) -> Result<Problem, String> {
    read_dimacs_announcing_to(filename, verbose, &mut io::stdout())
}

/// Same as [`read_dimacs`], but writes the "file being read" verbose
/// announcement to `announce_writer` instead of unconditionally to
/// stdout. This indirection exists so unit tests can verify the
/// announcement without capturing the process's real stdout.
fn read_dimacs_announcing_to<W: Write>(
    filename: &str,
    verbose: i32,
    announce_writer: &mut W,
) -> Result<Problem, String> {
    if verbose >= 1 {
        // The message is purely informational; a failure to write it
        // is not a reason to abort reading the problem.
        let _ = writeln!(announce_writer, "Reading CNF file: {filename}");
    }

    let file = File::open(filename)
        .map_err(|e| format!("could not open input file \"{filename}\": {e}"))?;
    let reader = io::BufReader::new(file);

    let mut num_vars: usize = 0;
    let mut num_clauses_stated: usize = 0;
    let mut header_seen = false;
    let mut clauses: Vec<Clause> = Vec::new();
    let mut current_clause: Clause = Vec::new();

    for (index, line_result) in reader.lines().enumerate() {
        let line_num = index + 1;
        let raw_line = line_result.map_err(|e| format!("error reading \"{filename}\": {e}"))?;
        let line = raw_line.trim();
        if line.is_empty() {
            continue;
        }

        match line.chars().next().expect("line is non-empty") {
            'c' => {
                // Comment line; ignore.
                continue;
            }
            '%' => {
                // End-of-clauses marker used by some benchmark files;
                // ignore everything from here to the end of the file.
                break;
            }
            'p' => {
                if header_seen {
                    return Err(format!("{filename}:{line_num}: duplicate problem line"));
                }
                let fields: Vec<&str> = line.split_whitespace().collect();
                if fields.len() != 4 || fields[0] != "p" || fields[1] != "cnf" {
                    return Err(format!(
                        "{filename}:{line_num}: malformed problem line \"{line}\", expected \"p cnf <vars> <clauses>\""
                    ));
                }
                num_vars = fields[2].parse::<usize>().map_err(|_| {
                    format!(
                        "{filename}:{line_num}: invalid number of variables \"{}\"",
                        fields[2]
                    )
                })?;
                num_clauses_stated = fields[3].parse::<usize>().map_err(|_| {
                    format!(
                        "{filename}:{line_num}: invalid number of clauses \"{}\"",
                        fields[3]
                    )
                })?;
                header_seen = true;
            }
            _ => {
                if !header_seen {
                    return Err(format!(
                        "{filename}:{line_num}: clause data found before problem line"
                    ));
                }
                for token in line.split_whitespace() {
                    let value: Literal = token.parse().map_err(|_| {
                        format!("{filename}:{line_num}: invalid literal \"{token}\"")
                    })?;
                    if value == 0 {
                        clauses.push(std::mem::take(&mut current_clause));
                        continue;
                    }
                    let variable = literal_var(value);
                    if variable > num_vars {
                        return Err(format!(
                            "{filename}:{line_num}: literal {value} refers to variable beyond declared count {num_vars}"
                        ));
                    }
                    current_clause.push(value);
                }
            }
        }
    }

    if !header_seen {
        return Err(format!(
            "{filename}: missing problem line (\"p cnf <vars> <clauses>\")"
        ));
    }
    if !current_clause.is_empty() {
        return Err(format!(
            "{filename}: final clause is not terminated with a 0"
        ));
    }
    if clauses.len() != num_clauses_stated {
        return Err(format!(
            "{filename}: problem line declares {num_clauses_stated} clauses but {} were found",
            clauses.len()
        ));
    }

    Ok(Problem { num_vars, clauses })
}

/// Writes the basic size parameters of a SAT problem to `writer`: the
/// number of variables, the number of clauses, and the total number
/// of literal occurrences across all clauses.
pub fn write_summary<W: Write>(problem: &Problem, writer: &mut W) -> io::Result<()> {
    writeln!(writer, "Number of variables: {}", problem.num_vars)?;
    writeln!(writer, "Number of clauses: {}", problem.num_clauses())?;
    writeln!(writer, "Number of literals: {}", problem.num_literals())?;
    Ok(())
}

/// Prints the basic size parameters of a SAT problem to stdout. See
/// [`write_summary`] for the information printed.
pub fn print_summary(problem: &Problem) {
    let mut stdout = io::stdout();
    // Writing to stdout should not fail under normal operation; if it
    // somehow does, there is nothing useful we can do about it here.
    let _ = write_summary(problem, &mut stdout);
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    static TEMP_FILE_COUNTER: AtomicUsize = AtomicUsize::new(0);

    /// A CNF file written to the system temp directory for the
    /// duration of a test; it is removed automatically when dropped.
    struct TempCnfFile {
        path: std::path::PathBuf,
    }

    impl TempCnfFile {
        fn new(contents: &str) -> Self {
            let unique = TEMP_FILE_COUNTER.fetch_add(1, Ordering::SeqCst);
            let mut path = std::env::temp_dir();
            path.push(format!(
                "vibe_sat_test_{}_{}.cnf",
                std::process::id(),
                unique
            ));
            std::fs::write(&path, contents).expect("failed to write temp cnf file");
            TempCnfFile { path }
        }

        fn path_str(&self) -> &str {
            self.path.to_str().expect("temp path is valid utf-8")
        }
    }

    impl Drop for TempCnfFile {
        fn drop(&mut self) {
            let _ = std::fs::remove_file(&self.path);
        }
    }

    #[test]
    fn test_literal_var() {
        assert_eq!(literal_var(5), 5);
        assert_eq!(literal_var(-5), 5);
        assert_eq!(literal_var(1), 1);
        assert_eq!(literal_var(-1), 1);
    }

    #[test]
    fn test_literal_is_negative() {
        assert!(!literal_is_negative(5));
        assert!(literal_is_negative(-5));
    }

    #[test]
    fn test_problem_num_clauses() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, -2], vec![3]],
        };
        assert_eq!(problem.num_clauses(), 2);
    }

    #[test]
    fn test_problem_num_literals() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![vec![1, -2, 3], vec![-1], vec![]],
        };
        assert_eq!(problem.num_literals(), 4);
    }

    #[test]
    fn test_read_dimacs_valid() {
        let file = TempCnfFile::new("c a simple example\np cnf 3 2\n1 -2 3 0\n-1 2 0\n");
        let problem = read_dimacs(file.path_str(), 0).expect("expected successful parse");
        assert_eq!(problem.num_vars, 3);
        assert_eq!(problem.num_clauses(), 2);
        assert_eq!(problem.num_literals(), 5);
    }

    #[test]
    fn test_read_dimacs_clause_spanning_multiple_lines() {
        let file = TempCnfFile::new("p cnf 3 1\n1 -2\n3 0\n");
        let problem = read_dimacs(file.path_str(), 0).expect("expected successful parse");
        assert_eq!(problem.num_clauses(), 1);
        assert_eq!(problem.num_literals(), 3);
    }

    #[test]
    fn test_read_dimacs_missing_file() {
        let result = read_dimacs("/nonexistent/path/does-not-exist.cnf", 0);
        assert!(result.is_err());
    }

    #[test]
    fn test_read_dimacs_missing_header() {
        let file = TempCnfFile::new("1 -2 0\n");
        assert!(read_dimacs(file.path_str(), 0).is_err());
    }

    #[test]
    fn test_read_dimacs_duplicate_header() {
        let file = TempCnfFile::new("p cnf 2 1\np cnf 2 1\n1 2 0\n");
        assert!(read_dimacs(file.path_str(), 0).is_err());
    }

    #[test]
    fn test_read_dimacs_invalid_literal() {
        let file = TempCnfFile::new("p cnf 2 1\n1 x 0\n");
        assert!(read_dimacs(file.path_str(), 0).is_err());
    }

    #[test]
    fn test_read_dimacs_literal_exceeds_var_count() {
        let file = TempCnfFile::new("p cnf 2 1\n1 3 0\n");
        assert!(read_dimacs(file.path_str(), 0).is_err());
    }

    #[test]
    fn test_read_dimacs_unterminated_clause() {
        let file = TempCnfFile::new("p cnf 2 1\n1 2\n");
        assert!(read_dimacs(file.path_str(), 0).is_err());
    }

    #[test]
    fn test_read_dimacs_clause_count_mismatch() {
        let file = TempCnfFile::new("p cnf 2 2\n1 2 0\n");
        assert!(read_dimacs(file.path_str(), 0).is_err());
    }

    #[test]
    fn test_read_dimacs_clause_before_header() {
        let file = TempCnfFile::new("1 2 0\np cnf 2 1\n");
        assert!(read_dimacs(file.path_str(), 0).is_err());
    }

    #[test]
    fn test_read_dimacs_empty_clause() {
        let file = TempCnfFile::new("p cnf 2 1\n0\n");
        let problem = read_dimacs(file.path_str(), 0).expect("expected successful parse");
        assert_eq!(problem.num_clauses(), 1);
        assert_eq!(problem.clauses[0].len(), 0);
    }

    #[test]
    fn test_read_dimacs_percent_terminator() {
        let file = TempCnfFile::new("p cnf 2 1\n1 2 0\n%\n0\n\n");
        let problem = read_dimacs(file.path_str(), 0).expect("expected successful parse");
        assert_eq!(problem.num_clauses(), 1);
    }

    #[test]
    fn test_read_dimacs_announces_filename_when_verbose() {
        let file = TempCnfFile::new("p cnf 1 1\n1 0\n");
        let mut announced = Vec::new();
        let result = read_dimacs_announcing_to(file.path_str(), 1, &mut announced);
        assert!(result.is_ok());
        let announced_text = String::from_utf8(announced).expect("valid utf-8");
        assert!(announced_text.contains(file.path_str()));
    }

    #[test]
    fn test_read_dimacs_silent_when_not_verbose() {
        let file = TempCnfFile::new("p cnf 1 1\n1 0\n");
        let mut announced = Vec::new();
        let result = read_dimacs_announcing_to(file.path_str(), 0, &mut announced);
        assert!(result.is_ok());
        assert!(announced.is_empty());
    }

    #[test]
    fn test_write_summary() {
        let problem = Problem {
            num_vars: 4,
            clauses: vec![vec![1, -2], vec![3, 4, -1]],
        };
        let mut buffer = Vec::new();
        write_summary(&problem, &mut buffer).expect("write_summary should not fail");
        let output = String::from_utf8(buffer).expect("valid utf-8");
        assert!(output.contains("Number of variables: 4"));
        assert!(output.contains("Number of clauses: 2"));
        assert!(output.contains("Number of literals: 5"));
    }
}
