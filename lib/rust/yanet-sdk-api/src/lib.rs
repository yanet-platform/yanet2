//! Native control-plane ownership and C export support for the Rust SDK.
//!
//! Attached agents build and release shared-memory configurations through the
//! native control-plane allocator, returning native error chains on failure.
//! This crate uses the standard library and unwinding so generated exports can
//! contain authored-method panics at the C boundary.

mod agent;
mod error;
mod ffi;

pub use agent::{Agent, Retained};
pub use error::{ApiError, ErrorKind};

/// Paths the exports of `#[yanet_sdk::api]` expand to; not part of the API.
#[doc(hidden)]
pub mod __private {
    pub use yanet_sys::{
        bindings::{agent, cp_module, yanet_error},
        cp::{Built, FfiRecord},
    };

    pub use crate::{
        Agent, ApiError,
        ffi::{build, c_str, free, record, slice_of},
    };
}
