//! Types the C side of an api crate's export hands in by pointer.

/// A C-compatible input value whose every bit pattern is valid.
///
/// User records require C representation and the input-record derive; primitive
/// integers and arrays also implement this contract. Padding is never read as a
/// field.
///
/// # Safety
///
/// The type must be sized, have no drop glue, use a C-compatible representation
/// and accept every bit pattern of its size. User records require #[repr(C)].
pub unsafe trait FfiRecord: Sized {}

macro_rules! ffi_record {
    ($($ty:ty),* $(,)?) => {
        $(
            // SAFETY: integer types, including pointer-sized integers, accept every bit
            // pattern.
            unsafe impl FfiRecord for $ty {}
        )*
    };
}

ffi_record!(u8, u16, u32, u64, i8, i16, i32, i64, usize);

// SAFETY: an array is its elements in sequence, each a record.
unsafe impl<T: FfiRecord, const N: usize> FfiRecord for [T; N] {}
