//! Layout contract of shared-memory types.
//!
//! A configuration is a `#[repr(C)]` struct implementing `ShmLayout`. The
//! trait is `unsafe`; this crate implements it for an allow-list of leaves,
//! arrays and the owned edges, and a struct gets it from
//! `#[derive(ShmLayout)]`, so every implementor is valid for every bit pattern
//! C may write, needs no drop and is aligned to at most 64. Padding may be
//! uninitialized and is never accessed as data.
//!
//! An IPv4 or IPv6 address of `core::net` is a leaf, so a configuration holds
//! the address a packet header view returns as it is. The standard library
//! documents no layout for an address: the build checks the one this crate
//! relies on, the octets in network order and nothing else.

use core::{
    mem::{align_of, transmute},
    net::{Ipv4Addr, Ipv6Addr},
    sync::atomic::{AtomicI8, AtomicI16, AtomicI32, AtomicI64, AtomicU8, AtomicU16, AtomicU32, AtomicU64},
};

use crate::rel::{ShmBox, ShmSlice};

/// Largest alignment a shared-memory type may require.
///
/// The block allocator aligns a block to at least the smaller of this and its
/// size rounded up to a power of two, so a type aligned to at most this is
/// aligned in any block of at least its size.
///
/// C's `MEMORY_BLOCK_MAX_ALIGN` (`common/memory_block.h`): blocks are
/// naturally aligned up to the 2 MiB hugepage, but a build with the address
/// sanitizer shifts each one by its 64-byte red zone. This crate has no C, so
/// `yanet-sys` asserts the two are equal.
#[doc(alias = "MEMORY_BLOCK_MAX_ALIGN")]
pub const MAX_ALIGN: usize = 64;

/// Marks a type that may live in shared memory.
///
/// # Safety
///
/// Implementors are `#[repr(C)]` structs, allow-listed leaves, arrays or the
/// owned edges such that every bit pattern is a valid value, dropping is a
/// no-op and the alignment is at most [`MAX_ALIGN`]. Padding may be
/// uninitialized; this trait permits typed field access, not viewing the
/// object representation as an initialized byte slice.
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

// The standard library documents no layout for its addresses, so the build
// proves the one the leaves below rely on: an address is its octets in
// network order and nothing else. A standard library that lays an address
// out in another way stops the build here.
const _: () = {
    const V4: [u8; 4] = [0x0a, 0x1b, 0x2c, 0x3d];
    const V6: [u8; 16] = [
        0x20, 0x01, 0x0d, 0xb8, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
    ];
    // SAFETY: the standard library makes an address of any four octets and
    // gives the same octets back, so the type has a value for every pattern
    // of four bytes. A transmute compiles only between types of one size,
    // and a type of four bytes with that many values has no invalid pattern
    // and no padding.
    let v4 = unsafe { transmute::<[u8; 4], Ipv4Addr>(V4) };
    // SAFETY: as above, over sixteen octets.
    let v6 = unsafe { transmute::<[u8; 16], Ipv6Addr>(V6) };
    // Arrays do not compare in a constant, the numbers of their bytes do.
    assert!(u32::from_be_bytes(v4.octets()) == u32::from_be_bytes(V4));
    assert!(u128::from_be_bytes(v6.octets()) == u128::from_be_bytes(V6));
    assert!(align_of::<Ipv4Addr>() == 1 && align_of::<Ipv6Addr>() == 1);
};

// SAFETY: an address is its octets and nothing else (proved above), so it
// accepts every bit pattern, has no padding, needs no drop and is aligned
// to 1.
unsafe impl ShmLayout for Ipv4Addr {}

// SAFETY: as for the IPv4 address.
unsafe impl ShmLayout for Ipv6Addr {}

// SAFETY: an array of layout elements has no padding between elements and
// no invalid pattern.
unsafe impl<T: ShmLayout, const N: usize> ShmLayout for [T; N] {}

// SAFETY: an offset accepts every bit pattern; the block it names is owned
// by the graph and aligned for `T` by the builder.
unsafe impl<T: ShmLayout> ShmLayout for ShmBox<T> {}

// SAFETY: as for the box, over the whole element range; an empty slice is
// NULL with a zero count.
unsafe impl<T: ShmLayout> ShmLayout for ShmSlice<T> {}
