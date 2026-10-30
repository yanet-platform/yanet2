//! The C bindings of the Rust module SDK, one module per C library.
//!
//! Holds the one bindgen output of the C headers (generated here only, so one
//! C struct is one Rust type). The modules mirror the C tree; a C structure a
//! module configuration embeds is a hand-written `#[repr(C)]` mirror whose
//! size, alignment and field offsets are asserted against the bindgen type.
//! `common` mirrors `common/`, `cp` mirrors `lib/controlplane/`. `no_std`
//! without `alloc`: module crates and dataplane plugins depend on it.

#![no_std]

#[allow(
    non_camel_case_types,
    non_upper_case_globals,
    non_snake_case,
    dead_code,
    unnecessary_transmutes,
    unsafe_op_in_unsafe_fn,
    clippy::all,
    clippy::undocumented_unsafe_blocks
)]
pub mod bindings {
    include!(concat!(env!("OUT_DIR"), "/bindings.rs"));
}

pub mod common;
pub mod counters;
pub mod cp;
pub mod dp;

// Test-only dependencies are visible to the library's own test target.
#[cfg(test)]
use yanet_sdk_testing as _;
