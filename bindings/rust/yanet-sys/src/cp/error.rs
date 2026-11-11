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
    InvalidName,
    /// A counter that a registrar's tagged registry took amid the registrar's
    /// counters other than through it, of a size the registrar does not
    /// vouch for; `YANET_ERROR_INVALID_ARGUMENT`.
    ForeignCounter,
    /// A key that two records of a hash index give.
    ///
    /// Reported as `YANET_ERROR_INVALID_ARGUMENT`.
    DuplicateKey {
        /// The position of the record that holds the key.
        first: u32,
        /// The position of the record refused.
        second: u32,
    },
    /// A record of a hash index at the position that marks a free slot,
    /// `u32::MAX`.
    ///
    /// Reported as `YANET_ERROR_INVALID_ARGUMENT`.
    InvalidPosition,
}

impl Refusal {
    /// The refusal's message, which `Display` writes and a C error frame
    /// carries.
    ///
    /// For a key given twice `Display` adds the positions of both records.
    pub fn message(self) -> &'static str {
        match self {
            Self::OutOfMemory => "the shared memory is exhausted",
            Self::BlockTooLarge => "an owned block larger than the largest block of the allocator",
            Self::InvalidRange => "a range ends before it starts",
            Self::InvalidMask => "a network mask is not contiguous",
            Self::TooManyItems => "more items than C counts",
            Self::RuleSetMismatch => "a join or a decode over rulesets of different sizes",
            Self::InvalidName => "a name is empty, holds a NUL or does not fit its C field",
            Self::ForeignCounter => "a counter registered amid a registrar's counters other than through it",
            Self::DuplicateKey { .. } => "a key of a hash index is given twice",
            Self::InvalidPosition => "a record of a hash index names the position of a free slot",
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
            | Self::InvalidName
            | Self::ForeignCounter
            | Self::DuplicateKey { .. }
            | Self::InvalidPosition => bindings::yanet_error_kind_YANET_ERROR_INVALID_ARGUMENT,
        }
    }
}

impl fmt::Display for Refusal {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        match *self {
            Self::DuplicateKey { first, second } => {
                write!(f, "{}, at positions {first} and {second}", self.message())
            }
            _ => f.write_str(self.message()),
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

    use super::Refusal;

    #[test]
    fn test_display_of_a_key_given_twice_names_both_positions() {
        assert_eq!(
            "a key of a hash index is given twice, at positions 3 and 7",
            Refusal::DuplicateKey { first: 3, second: 7 }.to_string()
        );
    }
}
