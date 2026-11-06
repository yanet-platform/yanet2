//! Optional values embedded in shared-memory graphs.
//!
//! An optional value is inline, so an absent one costs its bytes and no block:
//! a table that allocates when C initialises it, such as an LPM, is never
//! initialised when absent and owns nothing. A Rust `Option` has no layout C
//! or the shared-memory contract can rely on, so the option is a tag followed
//! by the value, the tag as wide and as aligned as the value.

use core::mem::{align_of, size_of};

use crate::{
    shm::ShmLayout,
    unit::{Unit, matches},
};

/// Value of type `T` that may be absent: a tag, then the value.
///
/// The value is held inline and is read only where the tag is set; an absent
/// value owns nothing, and its bytes, which a build zeroes, mean nothing. Like
/// the owning edges it has private fields, no constructor and no
/// `Copy`/`Clone`: a build writes it in place, the value before the tag, and
/// only before its configuration is published. A published configuration is
/// frozen, so a reader sees a complete value behind a set tag.
///
/// The option is `align_of::<T>()` bytes larger than `T` and aligned like `T`,
/// with no padding; an option of a value aligned to 64 costs a 64-byte tag.
/// The value is present when any byte of the tag is not zero: a build writes
/// a one in its first byte, and every other pattern of a set tag means the
/// same, so readers in another process need no more than that.
#[repr(C)]
pub struct ShmOption<T: ShmLayout> {
    tag: T::AlignUnit,
    value: T,
}

impl<T: ShmLayout> ShmOption<T> {
    /// Byte offset of the value inside the option, right after the tag.
    #[doc(hidden)]
    pub const VALUE_OFFSET: usize = size_of::<T::AlignUnit>();

    /// Refuses at compile time a value whose unit is not as wide and as
    /// aligned as it, which a hand-written layout could name.
    #[doc(hidden)]
    pub const UNIT_MATCHES: () = assert!(
        matches::<T>(),
        "the alignment unit of an optional value is not as wide and as aligned as the value"
    );

    /// The value, or `None` when it is absent.
    #[inline(always)]
    pub fn get(&self) -> Option<&T> {
        const { Self::UNIT_MATCHES };
        self.tag.is_set().then_some(&self.value)
    }

    /// Reports whether the value is present.
    #[inline(always)]
    pub fn is_some(&self) -> bool {
        self.get().is_some()
    }
}

/// Checks that the value of an option of `T` lies at its offset and that the
/// option is its tag and its value, without padding, aligned as the value.
const fn unpadded<T: ShmLayout>() -> bool {
    core::mem::offset_of!(ShmOption<T>, value) == ShmOption::<T>::VALUE_OFFSET
        && size_of::<ShmOption<T>>() == align_of::<T>() + size_of::<T>()
        && align_of::<ShmOption<T>>() == align_of::<T>()
}

const _: () = {
    assert!(unpadded::<u8>() && unpadded::<u16>() && unpadded::<u32>() && unpadded::<u64>());
    assert!(unpadded::<()>() && unpadded::<[u32; 3]>() && unpadded::<[u64; 0]>());
    assert!(unpadded::<ShmOption<u16>>() && unpadded::<crate::rel::ShmSlice<u8>>());
};
