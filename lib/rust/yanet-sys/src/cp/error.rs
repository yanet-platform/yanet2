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
    /// An LPM range that ends before it starts; `YANET_ERROR_INVALID_ARGUMENT`.
    InvalidRange,
    /// Device declarations repeat an index or do not form a contiguous table;
    /// `YANET_ERROR_INVALID_ARGUMENT`.
    InvalidDeviceTable,
}

impl Refusal {
    pub fn message(self) -> &'static str {
        match self {
            Self::OutOfMemory => "the shared memory is exhausted",
            Self::BlockTooLarge => "an owned block larger than the largest block of the allocator",
            Self::InvalidRange => "an LPM range ends before it starts",
            Self::InvalidDeviceTable => "device declarations are repeated or noncontiguous",
        }
    }
}

impl fmt::Display for Refusal {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> Result<(), fmt::Error> {
        f.write_str(self.message())
    }
}

impl Error for Refusal {}

/// Separates SDK refusals from failures reported by the owner.
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

impl<E: Error + 'static> Error for BuildError<E> {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Refused(refusal) => Some(refusal),
            Self::Owner(err) => Some(err),
        }
    }
}

#[cfg(test)]
mod tests {
    use core::error::Error;

    use super::{BuildError, Refusal};

    #[test]
    fn test_build_error_owner_source_preserves_leaf() {
        let error = BuildError::Owner(Refusal::InvalidRange);
        assert_eq!(
            Some(&Refusal::InvalidRange),
            error.source().unwrap().downcast_ref::<Refusal>()
        );
    }

    #[test]
    fn test_build_error_owner_source_preserves_nested_chain() {
        let error = BuildError::Owner(BuildError::Owner(Refusal::OutOfMemory));
        let source = error.source().unwrap();
        let owner = source.downcast_ref::<BuildError<Refusal>>().unwrap();
        assert_eq!(
            Some(&Refusal::OutOfMemory),
            owner.source().unwrap().downcast_ref::<Refusal>()
        );
    }

    #[test]
    fn test_build_error_refusal_source_preserves_leaf() {
        let error = BuildError::<Refusal>::Refused(Refusal::BlockTooLarge);
        assert_eq!(
            Some(&Refusal::BlockTooLarge),
            error.source().unwrap().downcast_ref::<Refusal>()
        );
    }
}
