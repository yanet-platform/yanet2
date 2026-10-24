//! Provenance root of the shared-memory mapping.
//!
//! Every pointer into shared memory the SDK dereferences is minted from a
//! root: a raw pointer received from C whose provenance covers the whole
//! mapping. Stored offsets and absolute addresses contribute only their
//! address (`with_addr`), so no exposed provenance is ever used. Under Miri a
//! root may instead resolve through a block table, one Rust allocation per
//! block, which lets Miri report cross-block accesses and accesses to
//! addresses no live block holds, such as those of a freed block. Only Miri
//! compiles that path, so no build of a product can reach it.

use core::ptr::NonNull;

/// Pointer factory for addresses inside the shared-memory mapping.
///
/// Computing a pointer is safe; dereferencing one is sound only under the
/// contract of the constructor that produced the root.
#[derive(Clone, Copy)]
pub struct ShmRoot {
    base: NonNull<u8>,
    #[cfg(miri)]
    blocks: Option<&'static dyn BlockLookup>,
}

impl ShmRoot {
    /// Root over the mapping C handed to the SDK.
    ///
    /// # Safety
    ///
    /// `base` must be a raw pointer received from C, never derived from a
    /// Rust reference, whose provenance covers every byte of the mapping
    /// any pointer of this root is used to access. The mapping is one
    /// contiguous mapping (environment assumption of the attach).
    #[inline(always)]
    pub const unsafe fn from_mapping(base: NonNull<u8>) -> Self {
        Self {
            base,
            #[cfg(miri)]
            blocks: None,
        }
    }

    /// Root resolving through a block table, for Miri tests.
    ///
    /// # Safety
    ///
    /// `fallback` provides the provenance of addresses no block contains, so
    /// accesses through them are reported by Miri.
    #[cfg(miri)]
    pub unsafe fn from_blocks(lookup: &'static dyn BlockLookup, fallback: NonNull<u8>) -> Self {
        Self { base: fallback, blocks: Some(lookup) }
    }

    /// Pointer with root provenance at an absolute address.
    #[inline(always)]
    pub fn ptr<T>(self, addr: usize) -> *mut T {
        #[cfg(miri)]
        if let Some(blocks) = self.blocks {
            if let Some(block) = blocks.find(addr) {
                return block.as_ptr().with_addr(addr).cast();
            }
        }
        self.base.as_ptr().with_addr(addr).cast()
    }

    /// Pointer with root provenance at `slot_addr + offset`, the target of
    /// a self-relative slot at `slot_addr` storing `offset`.
    #[inline(always)]
    pub fn rel<T>(self, slot_addr: usize, offset: isize) -> *mut T {
        self.ptr(slot_addr.wrapping_add_signed(offset))
    }
}

/// Block table of a test arena: finds the allocation holding an address.
#[cfg(miri)]
pub trait BlockLookup {
    /// Start of the live block containing `addr`, carrying that block's
    /// provenance, or `None`.
    fn find(&self, addr: usize) -> Option<NonNull<u8>>;
}
