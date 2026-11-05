//! Relative-pointer field types of shared-memory graphs.
//!
//! A slot stores `target - slot` (zero means NULL), exactly as the C
//! `ADDR_OF` family does. Every type here has private fields, no
//! constructor and no `Copy`/`Clone`: safe code only ever sees a shared
//! reference to a slot in place, so an offset is never separated from the
//! address it is relative to and never re-based by a move. Builders write
//! slots at their destination address. Resolution goes through a context
//! holding the provenance root (`Resolve`), passed to the edge's method, never
//! through the slot reference.
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
        let ptr = cx.shm_root().rel::<T>(slot, self.ptr.offset);
        debug_assert!(ptr.is_aligned(), "misaligned relative target");
        // SAFETY: a built non-null slice edge points at `len` aligned,
        // live elements inside the graph, frozen for 'g (contract of the
        // resolving context).
        unsafe { slice::from_raw_parts(ptr, self.len()) }
    }
}

impl<T> ShmBox<T> {
    /// The value the owned block holds, resolved through `cx`.
    #[inline(always)]
    pub fn get<'g>(&'g self, cx: &impl Resolve<'g>) -> &'g T {
        let slot = core::ptr::from_ref(self).addr();
        let ptr = cx.shm_root().rel::<T>(slot, self.ptr.offset);
        debug_assert!(ptr.is_aligned(), "misaligned owned block pointer");
        // SAFETY: a built owned edge points at an
        // aligned, live `T` frozen for 'g (trait contract); the root gives
        // the pointer provenance over it.
        unsafe { &*ptr }
    }
}

const _: () = {
    assert!(core::mem::size_of::<RelPtr<u64>>() == core::mem::size_of::<*const u8>());
    assert!(core::mem::size_of::<ShmBox<u64>>() == 8);
    assert!(core::mem::size_of::<ShmSlice<u64>>() == 16);
};
