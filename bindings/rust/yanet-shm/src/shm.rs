//! Layout contract of shared-memory types.
//!
//! A configuration is a `#[repr(C)]` struct implementing `ShmLayout`. The
//! trait is `unsafe`; this crate implements it for an allow-list of leaves,
//! arrays, the owned edges and the optional value, and a struct gets it from
//! `#[derive(ShmLayout)]`, so every implementor is valid for every bit pattern
//! C may write, has no implicit padding, needs no drop and is aligned to at
//! most 64.

use core::sync::atomic::{AtomicI8, AtomicI16, AtomicI32, AtomicI64, AtomicU8, AtomicU16, AtomicU32, AtomicU64};

use crate::{
    option::ShmOption,
    rel::{ShmBox, ShmSlice},
};

/// Largest alignment a shared-memory type may require.
///
/// The block allocator aligns a block to at least the smaller of this and its
/// size rounded up to a power of two, so a type aligned to at most this is
/// aligned in any block of at least its size.
///
/// C's `MEMORY_BLOCK_MAX_ALIGN` (`common/memory_block.h`): blocks are
/// naturally aligned up to the 2 MiB hugepage, but an ASan build shifts each
/// one by its 64-byte red zone. This crate has no C, so `yanet-sys` asserts
/// the two are equal.
pub const MAX_ALIGN: usize = 64;

/// Marks a type that may live in shared memory.
///
/// # Safety
///
/// Implementors are `#[repr(C)]` structs, allow-listed leaves, arrays, the
/// owned edges or the optional value such that every bit pattern is a valid
/// value, there is no implicit padding, dropping is a no-op and the alignment
/// is at most [`MAX_ALIGN`].
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

// SAFETY: a tag word accepts every bit pattern and the value is a layout type,
// so the option is valid for every pattern, needs no drop and is aligned to at
// most 8. A word-layout value follows the tag word with no padding and ends on
// a word, so the option has none.
unsafe impl<T: WordLayout> ShmLayout for ShmOption<T> {}

/// Marks a layout type made of whole 8-byte words: sized a multiple of 8 and
/// aligned to at most 8, so it follows a word with no padding and leaves none
/// after it.
///
/// [`ShmOption`] holds only such a value. A struct gets no implementation from
/// `#[derive(ShmLayout)]`; the SDK implements it for its word types.
///
/// # Safety
///
/// `size_of::<Self>()` is a multiple of 8 and `align_of::<Self>()` is at most
/// 8.
pub unsafe trait WordLayout: ShmLayout {}

macro_rules! word_leaf {
    ($($ty:ty),* $(,)?) => {$(
        // SAFETY: the leaf is 8 bytes aligned to 8, or empty.
        unsafe impl WordLayout for $ty {}
    )*};
}

word_leaf! { u64, i64, AtomicU64, AtomicI64, () }

// SAFETY: elements of whole words make an array of whole words aligned as an
// element.
unsafe impl<T: WordLayout, const N: usize> WordLayout for [T; N] {}

// SAFETY: one offset word.
unsafe impl<T: ShmLayout> WordLayout for ShmBox<T> {}

// SAFETY: an offset word and a count word.
unsafe impl<T: ShmLayout> WordLayout for ShmSlice<T> {}

// SAFETY: a tag word and a word-layout value.
unsafe impl<T: WordLayout> WordLayout for ShmOption<T> {}

const _: () = {
    assert!(size_of::<ShmBox<u8>>().is_multiple_of(8) && align_of::<ShmBox<u8>>() <= 8);
    assert!(size_of::<ShmSlice<u8>>().is_multiple_of(8) && align_of::<ShmSlice<u8>>() <= 8);
};
