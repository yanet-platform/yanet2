//! Bindings of `common/`: the LPM type with its lookup and the range it is
//! built from.
//!
//! The table is built by the C routines of `common/`, and the shared-memory
//! alignment bound is checked against the C block allocator's.

use yanet_shm::shm::MAX_ALIGN;

pub mod lpm;

const _: () = assert!(MAX_ALIGN == crate::bindings::MEMORY_BLOCK_MAX_ALIGN as usize);

/// Alignment of a block of 128 bytes or more from the C block allocator.
///
/// The allocator takes a block from the class of its size rounded up to a
/// power of two, whose blocks start at multiples of that size up to the huge
/// page. A build with sanitizer red zones hands out a block past its red zone
/// of [`MAX_ALIGN`] bytes, so only that much alignment is left.
pub const LINE_BLOCK_ALIGN: usize = if crate::bindings::ASAN_RED_ZONE == 0 {
    128
} else {
    MAX_ALIGN
};
