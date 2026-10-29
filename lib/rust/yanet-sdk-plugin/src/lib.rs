//! Runtime support for no_std Rust dataplane plugins.
//!
//! Tracked cdylib entrypoints export a module through the helper macro and use
//! the plugin profile with panic = "abort". The supplied panic handler calls
//! the dataplane executable's C abort routine, so a handler panic stops the
//! dataplane.

#![no_std]

pub use yanet_sys::export_module;

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
