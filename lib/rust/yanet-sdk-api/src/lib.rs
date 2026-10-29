//! Control-plane side of the Rust module SDK.
//!
//! This is a crate of its own because the C control-plane calls of a module's
//! API side live where a dataplane plugin never links them.
//!
//! An [`Agent`] is a handle on an agent attached to the shared memory. It owns
//! the configurations a module's API builds there: it implements the owner of
//! the configuration builder with the C routines a C module's API calls for
//! the same steps, and releases a configuration as those do. Every C failure
//! is an [`ApiError`].
//!
//! A `std` crate: the exports of `#[yanet_sdk::api]` catch the panic of a
//! method (`catch_unwind`) and hand it to C as an error, so an api crate is
//! built with unwinding.

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
