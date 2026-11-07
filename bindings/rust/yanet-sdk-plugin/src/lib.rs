//! Runtime of a dataplane plugin written in Rust.
//!
//! A plugin is a `#![no_std]` cdylib of one line, built under the `plugin`
//! profile (`panic = "abort"`, as a `no_std` cdylib cannot unwind):
//!
//! ```ignore
//! #![no_std]
//! yanet_sdk_plugin::export_module!(name = mine, module = mine::Mine);
//! ```
//!
//! The object types a module links ship in its plugin, listed in that line:
//!
//! ```ignore
//! yanet_sdk_plugin::export_module!(name = mine, module = mine::Mine, objects = [mine_table = mine::Table]);
//! ```
//!
//! An object type that ships without a module is a plugin of its own, of one
//! `export_object!` line.
//!
//! The crate supplies the panic handler a `no_std` binary needs. It aborts
//! through C `abort`, which the dataplane executable provides, so a panic in a
//! handler stops the dataplane as one in C code would.

#![no_std]

pub use yanet_sys::{export_module, export_object};

// Clippy checks the crate as a test even with `test = false`, and a test
// build links std and its own handler.
#[cfg(not(test))]
#[panic_handler]
fn panic(_: &core::panic::PanicInfo<'_>) -> ! {
    unsafe extern "C" {
        fn abort() -> !;
    }
    // SAFETY: C `abort` takes nothing and never returns.
    unsafe { abort() }
}
