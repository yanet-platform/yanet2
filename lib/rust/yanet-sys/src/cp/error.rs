//! Why the construction of a configuration failed.

use core::{error::Error, fmt};

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
}

impl Refusal {
    /// The refusal's message, which `Display` writes and a C error frame
    /// carries.
    pub fn message(self) -> &'static str {
        match self {
            Self::OutOfMemory => "the shared memory is exhausted",
            Self::BlockTooLarge => "an owned block larger than the largest block of the allocator",
            Self::InvalidRange => "a range ends before it starts",
            Self::InvalidMask => "a network mask is not contiguous",
            Self::TooManyItems => "more items than C counts",
            Self::RuleSetMismatch => "a join or a decode over rulesets of different sizes",
        }
    }
}

impl fmt::Display for Refusal {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.write_str(self.message())
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
