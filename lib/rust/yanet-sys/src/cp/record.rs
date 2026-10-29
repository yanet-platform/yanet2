//! Types the C side of an api crate's export hands in by pointer.

/// A `#[repr(C)]` type every bit pattern of which is a valid value: the bytes
/// behind a pointer C hands in are read as the type.
///
/// An api crate declares its records with `#[derive(FfiRecord)]` (in
/// `yanet-sdk-derive`) and the C header generated from them is what the C side
/// includes. A record has no padding that is read: padding is never a field.
///
/// # Safety
///
/// The type is `#[repr(C)]`, `Sized`, without drop glue, and every bit pattern
/// of its size is a valid value of it (so no `bool`, enum, reference, pointer
/// to a validity-restricted type or niche).
pub unsafe trait FfiRecord: Sized {}

macro_rules! ffi_record {
    ($($ty:ty),* $(,)?) => {
        $(
            // SAFETY: a fixed-width integer accepts every bit pattern.
            unsafe impl FfiRecord for $ty {}
        )*
    };
}

ffi_record!(u8, u16, u32, u64, i8, i16, i32, i64, usize);

// SAFETY: an array is its elements in sequence, each a record.
unsafe impl<T: FfiRecord, const N: usize> FfiRecord for [T; N] {}
