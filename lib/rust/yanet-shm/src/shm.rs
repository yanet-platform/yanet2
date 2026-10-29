//! Layout contract of shared-memory types.
//!
//! A configuration is a `#[repr(C)]` struct implementing `ShmLayout`. The
//! trait is `unsafe`; this crate implements it for an allow-list of leaves,
//! arrays and the owned edges, and a struct gets it from
//! `#[derive(ShmLayout)]`, so every implementor is valid for every bit pattern
//! C may write, has no implicit padding, needs no drop and is aligned to at
//! most 64.

use core::sync::atomic::{AtomicI8, AtomicI16, AtomicI32, AtomicI64, AtomicU8, AtomicU16, AtomicU32, AtomicU64};

use crate::rel::{ShmBox, ShmSlice};

/// Largest alignment a shared-memory type may require.
///
/// Allocation alignment is at least the smaller of this limit and the block
/// size rounded up to a power of two. Every permitted value fitting the block
/// therefore receives its required alignment. ASan red zones shift blocks by 64
/// bytes, limiting the guarantee even when the underlying hugepage is more
/// aligned. The C binding crate checks this limit against the allocator's
/// constant.
pub const MAX_ALIGN: usize = 64;

/// Marks a type that may live in shared memory.
///
/// # Safety
///
/// Implementors are `#[repr(C)]` structs, allow-listed leaves, arrays or the
/// owned edges such that every bit pattern is a valid value, there is no
/// implicit padding, dropping is a no-op and the alignment is at most
/// [`MAX_ALIGN`].
pub unsafe trait ShmLayout: Sized + 'static {}

macro_rules! plain_leaf {
    ($($ty:ty),* $(,)?) => {$(
        // SAFETY: the leaf accepts every bit pattern, has no padding, needs
        // no drop and is aligned to at most 8.
        unsafe impl ShmLayout for $ty {}
    )*};
}

plain_leaf! {
    u8, u16, u32, u64,
    i8, i16, i32, i64,
    AtomicU8, AtomicU16, AtomicU32, AtomicU64,
    AtomicI8, AtomicI16, AtomicI32, AtomicI64,
    (),
}

// SAFETY: an array of layout elements has no padding between elements and
// no invalid pattern.
unsafe impl<T: ShmLayout, const N: usize> ShmLayout for [T; N] {}

// SAFETY: an offset accepts every bit pattern; the block it names is owned
// by the graph and aligned for `T` by the builder.
unsafe impl<T: ShmLayout> ShmLayout for ShmBox<T> {}

// SAFETY: as for the box, over the whole element range; an empty slice is
// NULL with a zero count.
unsafe impl<T: ShmLayout> ShmLayout for ShmSlice<T> {}
