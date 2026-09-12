//! In-memory representation of an assignment of truth values to the
//! variables of a SAT problem.

use crate::cnf::Literal;
use rand::{Rng, RngExt};

/// The truth value of a single variable: `True`, `False`, or
/// `Unassigned`. This stage of vibe_sat only ever works with complete
/// assignments (every variable `True` or `False`), but the
/// three-valued representation is adopted from the start so that
/// later stages can represent partial assignments without changing
/// this data structure.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Value {
    False,
    True,
    Unassigned,
}

/// An assignment of a [`Value`] to every variable of a problem.
/// Indexed directly by variable number, 1 through `len() - 1`; index
/// 0 is unused so that a variable number can be used as the vector
/// index without adjustment.
pub type Assignment = Vec<Value>;

/// Returns an [`Assignment`] large enough to hold `num_vars`
/// variables, with every variable initially `Unassigned`.
pub fn new(num_vars: usize) -> Assignment {
    vec![Value::Unassigned; num_vars + 1]
}

/// Returns a new complete [`Assignment`] for `num_vars` variables,
/// where each variable is independently set to `True` or `False` with
/// equal probability, drawn from `rng`.
pub fn new_random<R: Rng>(num_vars: usize, rng: &mut R) -> Assignment {
    let mut assignment = vec![Value::Unassigned; num_vars + 1];
    for value in assignment.iter_mut().skip(1) {
        *value = if rng.random_bool(0.5) {
            Value::True
        } else {
            Value::False
        };
    }
    assignment
}

/// Returns whether `literal` evaluates to true under `assignment`: a
/// positive literal is true when its variable is `True`, a negative
/// literal is true when its variable is `False`. An `Unassigned`
/// variable makes any literal referencing it evaluate to false; this
/// is only meaningful for complete assignments, which is all this
/// stage produces.
pub fn literal_is_true(assignment: &Assignment, literal: Literal) -> bool {
    let value = assignment[crate::cnf::literal_var(literal)];
    if crate::cnf::literal_is_negative(literal) {
        value == Value::False
    } else {
        value == Value::True
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use rand::SeedableRng;
    use rand::rngs::StdRng;

    #[test]
    fn test_new_all_unassigned() {
        let a = new(3);
        assert_eq!(a.len(), 4);
        for value in a.iter().skip(1) {
            assert_eq!(*value, Value::Unassigned);
        }
    }

    #[test]
    fn test_new_random_produces_complete_assignment() {
        let mut rng = StdRng::seed_from_u64(1);
        let a = new_random(5, &mut rng);
        assert_eq!(a.len(), 6);
        for value in a.iter().skip(1) {
            assert!(*value == Value::True || *value == Value::False);
        }
    }

    #[test]
    fn test_new_random_is_deterministic_for_a_seeded_source() {
        let a = new_random(20, &mut StdRng::seed_from_u64(42));
        let b = new_random(20, &mut StdRng::seed_from_u64(42));
        assert_eq!(a, b);
    }

    #[test]
    fn test_literal_is_true_positive_literal() {
        let mut a = new(1);
        a[1] = Value::True;
        assert!(literal_is_true(&a, 1));
        a[1] = Value::False;
        assert!(!literal_is_true(&a, 1));
    }

    #[test]
    fn test_literal_is_true_negative_literal() {
        let mut a = new(1);
        a[1] = Value::False;
        assert!(literal_is_true(&a, -1));
        a[1] = Value::True;
        assert!(!literal_is_true(&a, -1));
    }
}
