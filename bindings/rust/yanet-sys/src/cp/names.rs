//! Names stored whole in fixed-size, NUL-terminated C fields.

use core::fmt::{self, Display, Formatter};

/// Why a name cannot be stored in its C field.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[non_exhaustive]
pub enum NameError {
    /// An obligatory name is empty.
    Empty,
    /// A NUL would make C read only a prefix of the name.
    InteriorNul,
    /// The bytes and their terminator do not fit the field.
    TooLong {
        /// The name's length in bytes.
        len: usize,
        /// The field's capacity, including the terminator.
        capacity: usize,
    },
}

impl Display for NameError {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match self {
            Self::Empty => f.write_str("the name is empty"),
            Self::InteriorNul => f.write_str("the name holds a NUL"),
            Self::TooLong { len, capacity } => {
                write!(
                    f,
                    "{len} bytes do not fit a {capacity}-byte C field with the terminator"
                )
            }
        }
    }
}

impl core::error::Error for NameError {}

/// Checks storage bounds and NULs, allowing an empty optional field.
pub fn validate_c_field(name: &str, capacity: usize) -> Result<(), NameError> {
    if name.len() >= capacity {
        return Err(NameError::TooLong { len: name.len(), capacity });
    }
    if name.as_bytes().contains(&0) {
        return Err(NameError::InteriorNul);
    }
    Ok(())
}

/// Checks an obligatory name, including room for its terminator.
pub fn validate_name(name: &str, capacity: usize) -> Result<(), NameError> {
    if name.is_empty() {
        return Err(NameError::Empty);
    }
    validate_c_field(name, capacity)
}

#[cfg(test)]
mod tests {
    use super::{NameError, validate_c_field, validate_name};

    #[test]
    fn test_name_reserves_one_byte_for_its_terminator() {
        assert_eq!(Ok(()), validate_name("abc", 4));
        assert_eq!(
            Err(NameError::TooLong { len: 4, capacity: 4 }),
            validate_name("abcd", 4)
        );
    }

    #[test]
    fn test_name_limits_utf8_bytes() {
        assert_eq!(Ok(()), validate_name("é", 3));
        assert_eq!(Err(NameError::TooLong { len: 2, capacity: 2 }), validate_name("é", 2));
    }

    #[test]
    fn test_name_refuses_interior_nul() {
        for name in ["\0", "a\0b", "ab\0"] {
            assert_eq!(Err(NameError::InteriorNul), validate_name(name, 4), "{name:?}");
        }
    }

    #[test]
    fn test_empty_field_is_allowed_only_when_optional() {
        assert_eq!(Ok(()), validate_c_field("", 1));
        assert_eq!(Err(NameError::Empty), validate_name("", 1));
    }

    #[test]
    fn test_zero_capacity_has_no_room_even_for_a_terminator() {
        assert_eq!(Err(NameError::TooLong { len: 0, capacity: 0 }), validate_c_field("", 0));
        assert_eq!(Err(NameError::TooLong { len: 1, capacity: 0 }), validate_name("a", 0));
    }
}
