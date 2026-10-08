//! The one Rust archive linked into yanet-dataplane.
//!
//! Each Rust staticlib carries its own copy of the Rust runtime symbols, so
//! two of them cannot share one executable. Every builtin Rust item is a
//! dependency of this crate instead, and its exported constructor reaches
//! the dataplane through this archive.

// A test build links std, which brings its own panic handler.
#![cfg_attr(not(test), no_std)]

use yanet_device_vxlan as _;
#[cfg(test)]
use yanet_dp_sys as _;

#[cfg(not(test))]
#[panic_handler]
fn panic(_info: &core::panic::PanicInfo<'_>) -> ! {
    yanet_dp_sys::abort()
}
