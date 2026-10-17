//! Runs the generated ctest suite.

#![allow(
    bad_style,
    dead_code,
    unused_imports,
    clippy::all,
    clippy::undocumented_unsafe_blocks,
    clippy::std_instead_of_core,
    unsafe_op_in_unsafe_fn
)]

#[path = "../../yanet-sys/src/ffi.rs"]
mod ffi;

use ffi::*;

include!(concat!(env!("OUT_DIR"), "/ctest_ffi.rs"));
