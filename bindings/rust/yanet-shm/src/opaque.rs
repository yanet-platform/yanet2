//! Bytes C may write while Rust holds a reference to the enclosing value.

use core::{cell::UnsafeCell, marker::PhantomPinned, mem::MaybeUninit};

/// Bytes of a C object Rust never reads or writes.
///
/// The `UnsafeCell` tells the compiler a shared reference to the enclosing
/// value does not freeze these bytes, so C may rewrite them (a registry
/// reference count, raised under the configuration lock and dropped by a
/// relaxed atomic, also without it; a memory-context sibling link) while
/// Rust holds `&` to the value. There is no read or write API: only the
/// address leaves, for C.
#[repr(transparent)]
pub struct Opaque<T> {
    value: UnsafeCell<MaybeUninit<T>>,
    _pinned: PhantomPinned,
}

impl<T> Opaque<T> {
    /// Raw pointer to the bytes, for handing to C.
    #[inline(always)]
    pub fn as_ptr(&self) -> *mut T {
        self.value.get().cast()
    }
}

// SAFETY: Rust never reads or writes the bytes behind an `Opaque`; sharing a
// reference across threads hands out only its address, and every access C
// makes is synchronised by C itself.
unsafe impl<T> Sync for Opaque<T> {}
// SAFETY: as above; nothing of `T` is moved or dropped by Rust.
unsafe impl<T> Send for Opaque<T> {}
