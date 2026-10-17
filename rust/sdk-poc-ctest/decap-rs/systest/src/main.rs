//! Runs the generated checks of the decap configuration mirror.

#![allow(
    bad_style,
    dead_code,
    unused_imports,
    clippy::all,
    clippy::undocumented_unsafe_blocks,
    clippy::std_instead_of_core,
    unsafe_op_in_unsafe_fn
)]

use core::mem::{align_of, size_of};

use yanet_sys::{ffi::cp_module, Lpm};

include!(concat!(env!("OUT_DIR"), "/shadow.rs"));
include!(concat!(env!("OUT_DIR"), "/checks.rs"));
include!(concat!(env!("OUT_DIR"), "/ctest_module.rs"));
