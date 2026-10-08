//! Rust replacement of the decap control-plane C API.
//!
//! Builds the decap configuration in the owner's shared memory with the C
//! allocator and LPM insert, through the generic sys builder, and validates
//! the whole graph before handing it over for publishing: every relative
//! edge must land on an aligned block inside the owner agent's arenas, page
//! counts must match the chunk directory, every child pointer must name a
//! page of the same LPM and leaf values must be in range. The dataplane
//! relies on exactly these invariants and checks none of them per packet.

#![forbid(unsafe_code)]

use core::{
    ffi::c_void,
    fmt::{self, Display, Formatter},
    ptr::NonNull,
};

pub use decap_dp::{Decap, DecapConfig};
use yanet_sys::builder::{ConfigBuilder, CpError, Owner};

/// Value every decap prefix maps to; the dataplane only tests presence.
pub const PREFIX_VALUE: u32 = 1;

/// One decap prefix as an inclusive big-endian address range.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Prefix {
    V4 { from: [u8; 4], to: [u8; 4] },
    V6 { from: [u8; 16], to: [u8; 16] },
}

/// Failure of a decap api call.
#[derive(Debug, PartialEq, Eq)]
pub enum ApiError {
    /// The request itself is malformed.
    InvalidArgument(String),
    /// Allocation, the C module setup, an insert or validation failed.
    Build(String),
}

impl Display for ApiError {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match self {
            Self::InvalidArgument(msg) => write!(f, "invalid argument: {msg}"),
            Self::Build(msg) => write!(f, "{msg}"),
        }
    }
}

impl From<CpError> for ApiError {
    fn from(err: CpError) -> Self {
        Self::Build(err.0)
    }
}

/// Builds a validated decap configuration named `name`; on success the
/// caller owns the returned module header and publishes it.
pub fn build<O: Owner>(owner: &O, name: &str, prefixes: &[Prefix]) -> Result<NonNull<c_void>, ApiError> {
    if name.is_empty() {
        return Err(ApiError::InvalidArgument("empty module name".into()));
    }
    for prefix in prefixes {
        let ordered = match prefix {
            Prefix::V4 { from, to } => from <= to,
            Prefix::V6 { from, to } => from <= to,
        };
        if !ordered {
            return Err(ApiError::InvalidArgument(format!(
                "range start is above its end in {prefix:?}"
            )));
        }
    }
    let mut config: ConfigBuilder<'_, Decap, O> = ConfigBuilder::new(owner, name)?;
    for prefix in prefixes {
        match prefix {
            Prefix::V4 { from, to } => config.insert(|c| &c.prefixes4, from, to, PREFIX_VALUE)?,
            Prefix::V6 { from, to } => config.insert(|c| &c.prefixes6, from, to, PREFIX_VALUE)?,
        }
    }
    Ok(config.finish()?)
}

// Test-only dependencies are visible to the library's own test target.
#[cfg(test)]
use yanet_sdk as _;
