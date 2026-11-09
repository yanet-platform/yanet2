//! Why the construction of a configuration failed.

use core::{error::Error, fmt};

use crate::bindings;

/// A request the SDK refuses on its own, before or instead of a C routine.
///
/// Each variant names the `yanet_error_kind` (`lib/errors/errors.h`) the
/// C-facing layer is to report it with, chosen by the kind's meaning; where
/// C has the same failure (memory exhausted, a size overflow), C tags it so.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[non_exhaustive]
pub enum Refusal {
    /// The owner's shared memory is exhausted, also under an embedded C
    /// object that ran out of it; `YANET_ERROR_RESOURCE_EXHAUSTED`.
    OutOfMemory,
    /// An owned block whose byte size overflows or exceeds the C block
    /// allocator's largest block; `YANET_ERROR_INVALID_ARGUMENT`.
    BlockTooLarge,
    /// A range that ends before it starts, of an LPM or of a classifier's
    /// rule; `YANET_ERROR_INVALID_ARGUMENT`.
    InvalidRange,
    /// A network mask of a classifier's rule that is not contiguous (for
    /// IPv6, in each 64-bit half), which the C compile would read as another
    /// network; `YANET_ERROR_INVALID_ARGUMENT`.
    InvalidMask,
    /// A set or a ruleset of more items than C counts, `u32::MAX`;
    /// `YANET_ERROR_INVALID_ARGUMENT`.
    TooManyItems,
    /// A join or a decode of stages or a projection over rulesets of
    /// different sizes; `YANET_ERROR_INVALID_ARGUMENT`.
    RuleSetMismatch,
    /// A name that is empty, holds a NUL or does not fit its C field with the
    /// terminator, which C would cut short; `YANET_ERROR_INVALID_ARGUMENT`.
    ///
    /// Out of the field's bounds the length is what is wrong; within them the
    /// name holds a NUL.
    InvalidName {
        /// The C field the name was to fill.
        field: NameField,
        /// The name's length in bytes.
        len: usize,
    },
    /// A counter that a registrar's tagged registry took amid the registrar's
    /// counters other than through it, of a size the registrar does not
    /// vouch for; `YANET_ERROR_INVALID_ARGUMENT`.
    ForeignCounter,
}

/// The C field a name fills, which bounds it: from one byte to one less than
/// the field, which keeps a byte for the terminator, and no NUL.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[non_exhaustive]
pub enum NameField {
    /// The type of a module, of `CP_MODULE_TYPE_LEN` bytes.
    ModuleType,
    /// The name of a module's configuration, of `CP_MODULE_NAME_LEN` bytes.
    ConfigName,
    /// The type of an object, of `CP_OBJECT_TYPE_LEN` bytes.
    ObjectType,
    /// The name of an object, of `CP_OBJECT_NAME_LEN` bytes.
    ObjectName,
    /// The name of a counter, of `COUNTER_NAME_LEN` bytes.
    CounterName,
    /// The tag of a counter registry, of `COUNTER_TAG_VALUE_LEN` bytes.
    CounterTag,
    /// The name of a device, of `CP_DEVICE_NAME_LEN` bytes.
    DeviceName,
}

impl NameField {
    /// The most bytes a name of the field has: the field's, less the
    /// terminator.
    pub const fn max_len(self) -> usize {
        let bytes = match self {
            Self::ModuleType => bindings::CP_MODULE_TYPE_LEN,
            Self::ConfigName => bindings::CP_MODULE_NAME_LEN,
            Self::ObjectType => bindings::CP_OBJECT_TYPE_LEN,
            Self::ObjectName => bindings::CP_OBJECT_NAME_LEN,
            Self::CounterName => bindings::COUNTER_NAME_LEN,
            Self::CounterTag => bindings::COUNTER_TAG_VALUE_LEN,
            Self::DeviceName => bindings::CP_DEVICE_NAME_LEN,
        };
        bytes as usize - 1
    }

    /// Accepts a name the field holds whole, as C reads it back.
    ///
    /// # Errors
    ///
    /// The name is empty, holds a NUL, where C would end it, or is longer than
    /// the field holds; the refusal carries the field and the name's length.
    pub fn check(self, name: &str) -> Result<(), Refusal> {
        if (1..=self.max_len()).contains(&name.len()) && !name.contains('\0') {
            return Ok(());
        }
        Err(Refusal::InvalidName { field: self, len: name.len() })
    }
}

impl fmt::Display for NameField {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.write_str(match self {
            Self::ModuleType => "module type",
            Self::ConfigName => "config name",
            Self::ObjectType => "object type",
            Self::ObjectName => "object name",
            Self::CounterName => "counter name",
            Self::CounterTag => "counter tag",
            Self::DeviceName => "device name",
        })
    }
}

impl Refusal {
    /// The message of the refusal's variant, the same for every refusal of
    /// it; `Display` writes what the refusal carries in its place, as a C
    /// error frame carries it.
    pub fn message(self) -> &'static str {
        match self {
            Self::OutOfMemory => "the shared memory is exhausted",
            Self::BlockTooLarge => "an owned block larger than the largest block of the allocator",
            Self::InvalidRange => "a range ends before it starts",
            Self::InvalidMask => "a network mask is not contiguous",
            Self::TooManyItems => "more items than C counts",
            Self::RuleSetMismatch => "a join or a decode over rulesets of different sizes",
            Self::InvalidName { .. } => "a name is empty, holds a NUL or does not fit its C field",
            Self::ForeignCounter => "a counter registered amid a registrar's counters other than through it",
        }
    }

    /// The `yanet_error_kind` the C-facing layer reports the refusal with.
    pub fn kind(self) -> bindings::yanet_error_kind {
        match self {
            Self::OutOfMemory => bindings::yanet_error_kind_YANET_ERROR_RESOURCE_EXHAUSTED,
            Self::BlockTooLarge
            | Self::InvalidRange
            | Self::InvalidMask
            | Self::TooManyItems
            | Self::RuleSetMismatch
            | Self::InvalidName { .. }
            | Self::ForeignCounter => bindings::yanet_error_kind_YANET_ERROR_INVALID_ARGUMENT,
        }
    }
}

impl fmt::Display for Refusal {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        match *self {
            Self::InvalidName { field, len } if (1..=field.max_len()).contains(&len) => {
                write!(f, "{field} holds a NUL")
            }
            Self::InvalidName { field, len } => {
                write!(f, "{field} has {len} bytes, must have from 1 to {}", field.max_len())
            }
            refusal => f.write_str(refusal.message()),
        }
    }
}

impl Error for Refusal {}

/// The error of building a configuration: a refusal of the SDK or a failure
/// of the owner.
#[derive(Debug, PartialEq, Eq)]
#[non_exhaustive]
pub enum BuildError<E> {
    /// The SDK refused the request.
    Refused(Refusal),
    /// A routine of the owner failed.
    Owner(E),
}

impl<E> From<Refusal> for BuildError<E> {
    fn from(refusal: Refusal) -> Self {
        Self::Refused(refusal)
    }
}

impl<E: fmt::Display> fmt::Display for BuildError<E> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        match self {
            Self::Refused(refusal) => fmt::Display::fmt(refusal, f),
            Self::Owner(err) => fmt::Display::fmt(err, f),
        }
    }
}

// Display shows the inner error, so the source is the inner one's.
impl<E: Error> Error for BuildError<E> {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Refused(refusal) => refusal.source(),
            Self::Owner(err) => err.source(),
        }
    }
}

#[cfg(test)]
mod tests {
    extern crate alloc;

    use alloc::string::ToString;

    use super::{NameField, Refusal};
    use crate::bindings;

    /// The fields beside the bytes of their C fields, the terminator's
    /// included.
    const FIELDS: [(NameField, u32); 7] = [
        (NameField::ModuleType, bindings::CP_MODULE_TYPE_LEN),
        (NameField::ConfigName, bindings::CP_MODULE_NAME_LEN),
        (NameField::ObjectType, bindings::CP_OBJECT_TYPE_LEN),
        (NameField::ObjectName, bindings::CP_OBJECT_NAME_LEN),
        (NameField::CounterName, bindings::COUNTER_NAME_LEN),
        (NameField::CounterTag, bindings::COUNTER_TAG_VALUE_LEN),
        (NameField::DeviceName, bindings::CP_DEVICE_NAME_LEN),
    ];

    /// verifies that a name filling its C field but for the terminator is
    /// accepted, in every field.
    #[test]
    fn test_name_field_check_accepts_a_name_filling_its_c_field() {
        for (field, bytes) in FIELDS {
            let name = "n".repeat(bytes as usize - 1);

            assert_eq!(Ok(()), field.check(&name), "{field}");
        }
    }

    /// verifies that a name leaving no byte of its C field for the terminator
    /// is refused with its field and length, in every field.
    #[test]
    fn test_name_field_check_refuses_a_name_past_its_c_field() {
        for (field, bytes) in FIELDS {
            let len = bytes as usize;

            assert_eq!(
                Err(Refusal::InvalidName { field, len }),
                field.check(&"n".repeat(len)),
                "{field}"
            );
        }
    }

    /// verifies that an empty name is refused, in every field.
    #[test]
    fn test_name_field_check_refuses_an_empty_name() {
        for (field, ..) in FIELDS {
            assert_eq!(Err(Refusal::InvalidName { field, len: 0 }), field.check(""), "{field}");
        }
    }

    /// verifies that a name holding a NUL, where C would end it, is refused,
    /// in every field.
    #[test]
    fn test_name_field_check_refuses_a_name_holding_a_nul() {
        for (field, ..) in FIELDS {
            assert_eq!(
                Err(Refusal::InvalidName { field, len: 5 }),
                field.check("ru\0le"),
                "{field}"
            );
        }
    }

    /// verifies that the message of a name of a wrong length names its field,
    /// the length and the field's bounds.
    #[test]
    fn test_refusal_display_names_the_field_and_bounds_of_a_long_name() {
        let refusal = Refusal::InvalidName {
            field: NameField::CounterName,
            len: 128,
        };

        assert_eq!(
            "counter name has 128 bytes, must have from 1 to 127",
            refusal.to_string()
        );
    }

    /// verifies that the message of an empty name names its field and the
    /// field's bounds.
    #[test]
    fn test_refusal_display_names_the_field_and_bounds_of_an_empty_name() {
        let refusal = Refusal::InvalidName { field: NameField::ConfigName, len: 0 };

        assert_eq!("config name has 0 bytes, must have from 1 to 79", refusal.to_string());
    }

    /// verifies that the message of a name of a length within its field's
    /// bounds says it holds a NUL.
    #[test]
    fn test_refusal_display_names_the_nul_of_a_name_within_bounds() {
        let refusal = Refusal::InvalidName { field: NameField::DeviceName, len: 5 };

        assert_eq!("device name holds a NUL", refusal.to_string());
    }
}
