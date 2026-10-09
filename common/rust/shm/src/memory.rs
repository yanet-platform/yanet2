//! Shared-memory layout mirrors for the read-side dataplane path.

use core::ffi::c_char;

use crate::offset::OffsetPtr;

/// Opaque `struct block_allocator` — never read by the dataplane.
#[repr(C)]
pub struct BlockAllocatorOpaque {
    _private: [u8; 0],
}

/// Mirror of `struct memory_context` (`common/memory.h`).
///
/// The context is embedded by value inside module configs and lookup
/// structures, so its size participates in their layouts; all three tree
/// links and the allocator handle are offset pointers.
#[repr(C)]
pub struct MemoryContext {
    pub block_allocator: OffsetPtr<BlockAllocatorOpaque>,
    pub balloc_count: usize,
    pub bfree_count: usize,
    pub balloc_size: usize,
    pub bfree_size: usize,
    pub name: [c_char; MEMORY_CONTEXT_NAME_SIZE],
    pub parent: OffsetPtr<MemoryContext>,
    pub first_child: OffsetPtr<MemoryContext>,
    pub next_sibling: OffsetPtr<MemoryContext>,
}

/// Title capacity of a context, counting the terminating zero.
pub const MEMORY_CONTEXT_NAME_SIZE: usize = 64;

/// A zeroed context is the valid "no allocations yet" state: every
/// embedded offset reads back NULL through `OffsetPtr::resolve`.
impl Default for MemoryContext {
    fn default() -> Self {
        // SAFETY: the all-zero bit pattern is a valid initialized context
        // (null allocator, null tree links, empty counters, empty name).
        unsafe { core::mem::zeroed() }
    }
}

const _: () = assert!(core::mem::size_of::<MemoryContext>() == 128);
