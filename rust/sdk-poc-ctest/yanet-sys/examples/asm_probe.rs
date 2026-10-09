//! Exported wrappers whose assembly `scripts/asm-compare.sh` compares with
//! the C `ADDR_OF` macros and the C `lpm_lookup`.

use core::ptr::NonNull;

use yanet_sys::{LpmView, Root, ffi::RelPtr};
use yanet_testkit as _;
use zerocopy as _;

/// Strict-provenance resolve with the NULL test, as `ADDR_OF`.
///
/// # Safety
///
/// `root` must satisfy the resolver contract for `slot`.
#[unsafe(no_mangle)]
#[inline(never)]
pub unsafe extern "C" fn rust_resolve(root: NonNull<u8>, slot: &RelPtr<u8>) -> *const u8 {
    // SAFETY: forwarded caller contract.
    let root = unsafe { Root::from_raw(root) };
    root.resolve(slot)
        .map_or(core::ptr::null(), |p| p.as_ptr().cast_const())
}

/// Strict-provenance resolve without the NULL test, as `ADDR_OF_NONNULL`.
///
/// # Safety
///
/// `root` must satisfy the resolver contract for `slot`.
#[unsafe(no_mangle)]
#[inline(never)]
pub unsafe extern "C" fn rust_resolve_nonnull(root: NonNull<u8>, slot: &RelPtr<u8>) -> *const u8 {
    // SAFETY: forwarded caller contract.
    let root = unsafe { Root::from_raw(root) };
    root.resolve_nonnull(slot)
}

/// One IPv6 lookup through an attached view.
#[unsafe(no_mangle)]
#[inline(never)]
pub extern "C" fn rust_lookup6(view: &LpmView<'_>, key: &[u8; 16]) -> u32 {
    view.lookup(key)
}

// Exported symbols are always emitted; nothing needs to call them.
fn main() {}
