//! Precomputes, for each variable of a SAT problem, which clauses
//! contain that variable positively and which contain it negatively.
//! Local-search solvers use this index to evaluate and apply the
//! effect of flipping a single variable in time proportional to that
//! variable's number of occurrences, rather than rescanning every
//! clause in the formula.

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
/// literal of every clause once.
pub fn build(problem: &Problem) -> Lists {
    let mut lists = Lists {
        positive: vec![Vec::new(); problem.num_vars + 1],
        negative: vec![Vec::new(); problem.num_vars + 1],
    };
    for (clause_index, clause) in problem.clauses.iter().enumerate() {
        for &literal in clause {
            let v = cnf::literal_var(literal);
            if cnf::literal_is_negative(literal) {
                lists.negative[v].push(clause_index);
            } else {
                lists.positive[v].push(clause_index);
            }
        }
    }
    lists
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
}
