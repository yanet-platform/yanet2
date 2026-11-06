//! Alignment units: for every alignment a shared-memory type may have, a
//! blob of bytes exactly that wide and aligned. A type's unit tags an
//! optional value of it, which then follows the tag with no padding.
//!
//! The derive and hand-written layouts name units through `Align`; nothing
//! else should.

use crate::shm::{MAX_ALIGN, ShmLayout};

mod sealed {
    pub trait Sealed {}
}

/// Unit of an alignment: as many bytes as it is aligned to, set when any byte
/// is not zero, which is the contract readers in other processes rely on.
///
/// Sealed: the units are [`Unit1`] to [`Unit64`], one per power of two up to
/// [`MAX_ALIGN`].
///
/// # Safety
///
/// Every bit pattern is a valid value, there is no padding and no drop glue,
/// the size equals the alignment, [`Self::ZERO`] is all zero bytes and
/// [`Self::SET`] has its first byte 1 and every other byte zero.
pub unsafe trait Unit: sealed::Sealed + Copy + 'static {
    /// The unit with every byte zero.
    const ZERO: Self;
    /// The unit with its first byte 1 and every other byte zero.
    const SET: Self;

    /// Reports whether any byte is not zero.
    fn is_set(&self) -> bool;
}

macro_rules! units {
    ($($name:ident: $n:literal),*) => {$(
        #[doc = concat!("Unit of an alignment of ", stringify!($n), " bytes.")]
        #[derive(Clone, Copy)]
        #[repr(C, align($n))]
        pub struct $name([u8; $n]);

        impl sealed::Sealed for $name {}

        // SAFETY: bytes accept every pattern and need no drop, and the array
        // fills the alignment, so there is no padding (asserted below).
        unsafe impl Unit for $name {
            const ZERO: Self = Self([0; $n]);
            const SET: Self = {
                let mut bytes = [0; $n];
                bytes[0] = 1;
                Self(bytes)
            };

            #[inline(always)]
            fn is_set(&self) -> bool {
                let Self(bytes) = self;
                *bytes != [0; $n]
            }
        }

        impl Alignment for Align<$n> {
            type Unit = $name;
        }

        const _: () = {
            assert!(size_of::<$name>() == $n && align_of::<$name>() == $n);
            assert!($n <= MAX_ALIGN);
        };
    )*};
}

/// Alignment of `N` bytes, which names its unit through [`Alignment`].
pub struct Align<const N: usize>;

/// Maps an alignment to its unit; implemented for every power of two up to
/// [`MAX_ALIGN`].
#[diagnostic::on_unimplemented(
    message = "no shared-memory alignment unit for `{Self}`",
    note = "a shared-memory type is aligned to at most 64 bytes, the block allocator's MAX_ALIGN"
)]
pub trait Alignment {
    /// The unit as wide and as aligned as the alignment.
    type Unit: Unit;
}

units!(Unit1: 1, Unit2: 2, Unit4: 4, Unit8: 8, Unit16: 16, Unit32: 32, Unit64: 64);

/// Reports whether the unit of `T` is as wide and as aligned as `T`, the part
/// of the layout contract a hand-written implementation states itself.
pub const fn matches<T: ShmLayout>() -> bool {
    size_of::<T::AlignUnit>() == align_of::<T>() && align_of::<T::AlignUnit>() == align_of::<T>()
}
