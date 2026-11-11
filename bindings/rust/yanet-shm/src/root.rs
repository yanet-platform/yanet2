//! Provenance root of the shared-memory mapping.
//!
//! Every pointer into shared memory the SDK dereferences is minted from a
//! root: a raw pointer whose provenance covers the whole mapping. Stored
//! offsets and absolute addresses contribute only their address
//! (`with_addr`), so no exposed provenance is ever used.

use core::ptr::NonNull;

/// Pointer factory for addresses inside the shared-memory mapping.
///
/// Computing a pointer is safe; dereferencing one is sound only under the
/// contract of the constructor that produced the root.
#[derive(Clone, Copy)]
pub struct ShmRoot {
    base: NonNull<u8>,
}

impl ShmRoot {
    /// Root over the shared mapping.
    ///
    /// # Safety
    ///
    /// `base` must be a raw pointer, never derived from a Rust reference,
    /// whose provenance covers every byte of the one contiguous mapping any
    /// pointer of this root is used to access (in a product, C's shared
    /// mapping).
    #[inline(always)]
    pub const unsafe fn from_mapping(base: NonNull<u8>) -> Self {
        Self { base }
    }

    /// Pointer with root provenance at an absolute address.
    #[inline(always)]
    pub fn ptr<T>(self, addr: usize) -> *mut T {
        self.base.as_ptr().with_addr(addr).cast()
    }

    /// Pointer with root provenance at `slot_addr + offset`, the target of
    /// a self-relative slot at `slot_addr` storing `offset`.
    #[inline(always)]
    pub fn rel<T>(self, slot_addr: usize, offset: isize) -> *mut T {
        self.ptr(slot_addr.wrapping_add_signed(offset))
    }
}
