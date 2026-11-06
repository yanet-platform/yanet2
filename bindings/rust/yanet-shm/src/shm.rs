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
    unit::{Align, Alignment, Unit, matches},
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
///
/// [`Self::AlignUnit`] is exactly `align_of::<Self>()` bytes in size and in
/// alignment.
pub unsafe trait ShmLayout: Sized + 'static {
    /// A blob of bytes exactly as wide and as aligned as the type, which tags
    /// an optional value of it.
    ///
    /// The derive names it; a hand-written layout names
    /// `<Align<{ align_of::<Self>() }> as Alignment>::Unit` from the hidden
    /// `unit` module, or, if generic with an alignment its parameters do not
    /// change, that of one instance. Only the optional value may hold the unit
    /// of another type: an edge holding its target's unit makes the type's
    /// alignment depend on itself, which rustc refuses as a cycle.
    type AlignUnit: Unit;
}

/// The unit of the alignment of `$ty`, a type without generic parameters.
macro_rules! unit_of {
    ($ty:ty) => {
        <Align<{ align_of::<$ty>() }> as Alignment>::Unit
    };
}

macro_rules! plain_leaf {
    ($($ty:ty),* $(,)?) => {$(
        // SAFETY: the leaf accepts every bit pattern, has no padding, needs
        // no drop and is aligned to at most 8; its unit is that of its
        // alignment.
        unsafe impl ShmLayout for $ty {
            type AlignUnit = unit_of!($ty);
        }

        const _: () = assert!(matches::<$ty>() && !core::mem::needs_drop::<$ty>());
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
// no invalid pattern, and is aligned as an element, also when empty.
unsafe impl<T: ShmLayout, const N: usize> ShmLayout for [T; N] {
    type AlignUnit = T::AlignUnit;
}

// SAFETY: an offset accepts every bit pattern; the block it names is owned
// by the graph and aligned for `T` by the builder. The value is the offset
// whatever `T` is, so every box has the alignment of the one named.
unsafe impl<T: ShmLayout> ShmLayout for ShmBox<T> {
    type AlignUnit = unit_of!(ShmBox<u8>);
}

// SAFETY: as for the box, over the whole element range; an empty slice is
// NULL with a zero count.
unsafe impl<T: ShmLayout> ShmLayout for ShmSlice<T> {
    type AlignUnit = unit_of!(ShmSlice<u8>);
}

// SAFETY: a unit accepts every bit pattern and the value is a layout type, so
// the option is valid for every pattern and needs no drop. The unit is as wide
// and as aligned as the value, which this impl takes on trust from the value's
// own unsafe layout contract and every reading, writing and releasing path
// checks at compile time; so the value follows it with no padding, the option
// ends on the value and is aligned as it, and the unit is the option's too.
unsafe impl<T: ShmLayout> ShmLayout for ShmOption<T> {
    type AlignUnit = T::AlignUnit;
}

const _: () = {
    assert!(matches::<[u32; 3]>() && matches::<[u64; 0]>());
    assert!(matches::<ShmBox<[u8; 3]>>() && matches::<ShmSlice<u16>>());
};
