//! Raw-pointer factory retaining the provenance of a supplied base.
//!
//! Stored offsets and absolute addresses contribute only their address
//! (`with_addr`), so no exposed provenance is ever used. The factory proves
//! no allocation validity, liveness or graph publication. Graph tokens,
//! owners and unsafe dereference contracts establish those obligations.

use core::ptr::NonNull;

/// Pointer factory retaining the provenance of `base`.
///
/// It only computes raw pointers. Dereferencing one requires a separate
/// contract establishing its provenance, validity, lifetime and aliasing.
#[derive(Clone, Copy)]
pub struct ShmRoot {
    base: NonNull<u8>,
}

impl ShmRoot {
    /// Stores a base without accessing memory or validating a mapping.
    ///
    /// The factory preserves exactly the provenance of `base`. It does not
    /// establish that the pointer covers a mapping or that any target is live.
    ///
    /// ```
    /// #![forbid(unsafe_code)]
    /// use core::ptr::NonNull;
    ///
    /// use yanet_shm::root::ShmRoot;
    /// let root = ShmRoot::from_mapping(NonNull::dangling());
    /// assert_eq!(root.ptr::<u8>(128).addr(), 128);
    /// ```
    #[inline(always)]
    pub const fn from_mapping(base: NonNull<u8>) -> Self {
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
