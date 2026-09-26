//! Precomputes, for each variable of a SAT problem, which clauses
//! contain that variable positively and which contain it negatively.
//! Local-search solvers use this index to evaluate and apply the
//! effect of flipping a single variable in time proportional to that
//! variable's number of occurrences, rather than rescanning every
//! clause in the formula.

use std::time::{Duration, Instant};

use crate::cnf::{self, Problem};

/// Holds, for every variable of a problem, the indices of the clauses
/// in which that variable appears positively (as a literal `+v`) and
/// the indices of the clauses in which it appears negatively (as a
/// literal `-v`). Both vectors are indexed directly by variable
/// number, 1 through `num_vars`; index 0 is unused.
pub struct Lists {
    pub positive: Vec<Vec<usize>>,
    pub negative: Vec<Vec<usize>>,
}

/// Computes the occurrence [`Lists`] for `problem` by scanning every
/// literal of every clause once. Equivalent to
/// [`build_with_deadline`] with no time limit, for every caller that
/// doesn't need one (every caller except `cdcl::bootstrap`'s own
/// time-bounded bootstrap path -- see `build_with_deadline`'s doc
/// comment).
pub fn build(problem: &Problem) -> Lists {
    build_with_deadline(problem, None, Instant::now()).0
}

/// [`build`], plus a `time_limit`/`start_time` pair (`None`/any
/// `Instant` meaning no limit, the same convention `dfs::run`'s/
/// `cdcl::run_loop`'s own search loops use) checked once per clause:
/// the returned `bool` is true the moment the deadline is noticed, in
/// which case the returned [`Lists`] is a partial, incomplete index
/// that must not be used for anything.
///
/// STAGE52.md: added so `cdcl::bootstrap` -- which already builds a
/// `Lists` for `rephase_from_walksat`'s benefit -- doesn't have an
/// unbounded, uninterruptible single pass sitting after its own
/// now-interruptible `unit_propagate` and watch-selection steps
/// (`REPORT35.md`'s own disclosed limitation; see
/// `reports/REPORT52.md`). Every other caller (`dfs` no longer needs a
/// `Lists` at all since STAGE46.md; `hillclimb`, `ws`, and `cdcl`'s own
/// `reduce_clause_database` all call the plain, unbounded [`build`])
/// is unaffected.
pub fn build_with_deadline(
    problem: &Problem,
    time_limit: Option<Duration>,
    start_time: Instant,
) -> (Lists, bool) {
    let mut lists = Lists {
        positive: vec![Vec::new(); problem.num_vars + 1],
        negative: vec![Vec::new(); problem.num_vars + 1],
    };
    for (clause_index, clause) in problem.clauses.iter().enumerate() {
        if let Some(limit) = time_limit
            && start_time.elapsed() >= limit
        {
            return (lists, true);
        }
        for &literal in clause {
            let v = cnf::literal_var(literal);
            if cnf::literal_is_negative(literal) {
                lists.negative[v].push(clause_index);
            } else {
                lists.positive[v].push(clause_index);
            }
        }
    }
    (lists, false)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_build_positive_and_negative_occurrences() {
        let problem = Problem {
            num_vars: 3,
            clauses: vec![
                vec![1, -2], // clause 0
                vec![-1, 3], // clause 1
                vec![2, 3],  // clause 2
            ],
        };

        let lists = build(&problem);

        assert_eq!(lists.positive[1], vec![0]);
        assert_eq!(lists.negative[1], vec![1]);
        assert_eq!(lists.positive[2], vec![2]);
        assert_eq!(lists.negative[2], vec![0]);
        assert_eq!(lists.positive[3], vec![1, 2]);
        assert!(lists.negative[3].is_empty());
    }

    #[test]
    fn test_build_tautological_clause_appears_in_both_lists() {
        let problem = Problem {
            num_vars: 1,
            clauses: vec![vec![1, -1]],
        };

        let lists = build(&problem);

        assert_eq!(lists.positive[1], vec![0]);
        assert_eq!(lists.negative[1], vec![0]);
    }

    #[test]
    fn test_build_empty_problem() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![],
        };
        let lists = build(&problem);
        assert_eq!(lists.positive.len(), 3);
        assert_eq!(lists.negative.len(), 3);
    }

    /// STAGE52.md's core mechanism, verified directly: with an
    /// already-elapsed deadline (a zero time_limit against
    /// Instant::now()), build_with_deadline must report timed_out =
    /// true before scanning even the first clause -- the deadline
    /// check runs at the top of each iteration, before that clause's
    /// own literals are recorded.
    #[test]
    fn test_build_with_deadline_reports_timed_out_before_first_clause() {
        let problem = Problem {
            num_vars: 2,
            clauses: vec![vec![1], vec![2]],
        };
        let already_elapsed = Duration::from_secs(0);

        let (lists, timed_out) =
            build_with_deadline(&problem, Some(already_elapsed), Instant::now());
        assert!(
            timed_out,
            "expected timed_out = true with an already-elapsed deadline"
        );
        assert!(
            lists.positive[1].is_empty(),
            "positive[1] = {:?}, want empty (deadline noticed before the first clause was even scanned)",
            lists.positive[1]
        );
        assert!(
            lists.positive[2].is_empty(),
            "positive[2] = {:?}, want empty",
            lists.positive[2]
        );
    }
}
