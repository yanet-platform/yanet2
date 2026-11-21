//! Relative-pointer field types of shared-memory graphs.
//!
//! A slot stores `target - slot` (zero means NULL), exactly as the C
//! `ADDR_OF` family does. Owning edges have private fields and no safe
//! constructor or `Copy`/`Clone`: safe code only sees them in place, so an
//! offset is never separated from the address it is relative to. Builders
//! write edges at their destination address. Resolution goes through a
//! context holding the provenance root (`Resolve`), passed to the edge's
//! method, never through the slot reference.
//!
//! Owning edges (`ShmBox`, `ShmSlice`) are the configuration field types:
//! they define the blocks of a configuration graph. `RelPtr` is the slot type
//! of C offset pointers the SDK reads through its mirrors of C structs, such as
//! the internal pointers of C data structures; it is not a configuration field
//! type, since a configuration graph owns every block it points to.

use core::{marker::PhantomData, slice};

use crate::resolve::Resolve;

/// Nullable relative pointer: the slot of a C offset pointer, and the
/// pointer word of the owning edges.
#[repr(C)]
pub struct RelPtr<T> {
    offset: isize,
    _target: PhantomData<T>,
}

impl<T> RelPtr<T> {
    /// Wraps a stored offset without granting access to its target.
    #[doc(hidden)]
    pub const fn from_offset(offset: isize) -> Self {
        Self { offset, _target: PhantomData }
    }

    /// Stored offset; the target is the slot address plus the offset.
    #[inline(always)]
    pub fn offset(&self) -> isize {
        self.offset
    }
}

/// Owned block holding one `T`, non-null in a built graph.
///
/// The block size comes from the type, as C frees a structure by its size.
#[repr(C)]
pub struct ShmBox<T> {
    ptr: RelPtr<T>,
}

/// Owned block of `len` consecutive `T`, NULL exactly when empty.
#[repr(C)]
pub struct ShmSlice<T> {
    ptr: RelPtr<T>,
    len: u64,
}

impl<T> ShmSlice<T> {
    /// Forms the metadata to write into a particular slice slot.
    ///
    /// # Safety
    ///
    /// The value may only be used after placement in that slot. The offset
    /// is relative to the slot and must name a live, aligned block of exactly
    /// `len` elements in the issuing context. Each element must satisfy its
    /// shared-memory invariants at its final address, including any nested
    /// links. The slot alone owns the block. When empty, both `offset` and
    /// `len` must be zero.
    #[doc(hidden)]
    pub const unsafe fn from_offset(offset: isize, len: usize) -> Self {
        Self {
            ptr: RelPtr::from_offset(offset),
            len: len as u64,
        }
    }

    /// Byte offset of the element count inside the slot pair.
    pub const LEN_OFFSET: usize = core::mem::offset_of!(ShmSlice<u8>, len);

    /// Element count.
    #[inline(always)]
    pub fn len(&self) -> usize {
        self.len as usize
    }

    /// Reports whether the slice is empty.
    #[inline(always)]
    pub fn is_empty(&self) -> bool {
        self.len == 0
    }

    /// Stored offset of the first element.
    #[inline(always)]
    pub fn offset(&self) -> isize {
        self.ptr.offset
    }

    /// The elements of the owned slice, resolved through `cx`.
    #[inline(always)]
    pub fn as_slice<'g>(&'g self, cx: &impl Resolve<'g>) -> &'g [T] {
        if self.ptr.offset == 0 {
            return &[];
        }
        let slot = core::ptr::from_ref(self).addr();
        let ptr = cx.graph().root().rel::<T>(slot, self.ptr.offset);
        debug_assert!(ptr.is_aligned(), "misaligned relative target");
        // SAFETY: a built non-null slice edge points at `len` aligned,
        // live elements inside the graph, frozen for 'g (contract of the
        // graph token).
        unsafe { slice::from_raw_parts(ptr, self.len()) }
    }
}

impl<T> ShmBox<T> {
    /// Stored offset of the owned block.
    #[doc(hidden)]
    #[inline(always)]
    pub fn offset(&self) -> isize {
        self.ptr.offset()
    }

    /// The value the owned block holds, resolved through `cx`.
    #[inline(always)]
    pub fn get<'g>(&'g self, cx: &impl Resolve<'g>) -> &'g T {
        let slot = core::ptr::from_ref(self).addr();
        let ptr = cx.graph().root().rel::<T>(slot, self.ptr.offset);
        debug_assert!(ptr.is_aligned(), "misaligned owned block pointer");
        // SAFETY: a built owned edge points at an
        // aligned, live `T` frozen for 'g (graph token contract). The root
        // gives the pointer provenance over it.
        unsafe { &*ptr }
    }
}

const _: () = {
    assert!(size_of::<RelPtr<u64>>() == size_of::<*const u8>());
    assert!(size_of::<ShmBox<u64>>() == 8);
    assert!(size_of::<ShmSlice<u64>>() == 16);
};
