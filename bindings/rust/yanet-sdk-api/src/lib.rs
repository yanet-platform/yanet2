//! Control-plane side of the Rust module SDK.
//!
//! This is a crate of its own because the C control-plane calls of a module's
//! API side live where a dataplane plugin never links them.
//!
//! An [`Agent`] is a handle on an agent attached to the shared memory. It owns
//! the configurations a module's API builds there: it implements the owner of
//! the configuration builder with the C routines a C module's API calls for
//! the same steps, and releases a configuration as those do. It owns the
//! objects built there in the same way, which are released as a C object's
//! owner releases them and read through [`Agent::read`]. What the dataplane
//! runs, the configuration a generation publishes under a name and the objects
//! it links, is read through [`Agent::read_published`] under a pin of that
//! generation. Every C failure is an [`Error`].
//!
//! A `std` crate: the exports of `#[yanet_sdk::api]` catch the panic of a
//! method (`catch_unwind`) and hand it to C as an error, so an api crate is
//! built with unwinding.

mod agent;
mod error;
mod ffi;

pub use agent::{Agent, ObjectCx, ObjectKey, PublishedCx, Retained};
pub use error::{Error, ErrorKind};

/// Paths the exports of `#[yanet_sdk::api]` expand to; not part of the API.
#[doc(hidden)]
pub mod __private {
    pub use yanet_sys::{
        bindings::{agent, cp_module, cp_object, yanet_error},
        cp::{Built, BuiltObject, FfiRecord},
    };

    pub use crate::{
        Agent, Error,
        ffi::{build, c_str, free, handle, out_record, out_slice, query, record, slice_of, zeroed, zeroed_slice},
    };
}

#[cfg(test)]
use yanet_sdk as _;
