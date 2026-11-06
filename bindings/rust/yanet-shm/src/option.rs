//! Optional values embedded in shared-memory graphs.
//!
//! An optional value is inline, so an absent one costs its bytes and no block:
//! a table that allocates when C initialises it, such as an LPM, is never
//! initialised when absent and owns nothing. A Rust `Option` has no layout C
//! or the shared-memory contract can rely on, so the option is a tag byte
//! followed by the value.

use core::mem::offset_of;

use crate::shm::ShmLayout;

/// Value of type `T` that may be absent: a tag byte, then the value.
///
/// The value is present when the tag is not zero, and is read only then; an
/// absent value owns nothing. The value follows the tag at its own alignment,
/// so the option is aligned like `T`; the padding between them is never read.
/// It has no constructor and no `Copy`/`Clone`: a build writes it in place,
/// the value before the tag, only before its configuration is published, and
/// a published configuration is frozen, so a reader sees a complete value
/// behind a set tag.
#[repr(C)]
pub struct ShmOption<T: ShmLayout> {
    tag: u8,
    value: T,
}

impl<T: ShmLayout> ShmOption<T> {
    /// Byte offset of the value inside the option; the tag is at offset 0.
    #[doc(hidden)]
    pub const VALUE_OFFSET: usize = offset_of!(Self, value);

    /// The value, or `None` when it is absent.
    #[inline(always)]
    pub fn get(&self) -> Option<&T> {
        (self.tag != 0).then_some(&self.value)
    }

    /// Reports whether the value is present.
    #[inline(always)]
    pub fn is_some(&self) -> bool {
        self.tag != 0
    }
}

const _: () = {
    use core::mem::{align_of, size_of};

    assert!(size_of::<ShmOption<u8>>() == 2 && size_of::<ShmOption<u32>>() == 8);
    assert!(size_of::<ShmOption<u64>>() == 16 && align_of::<ShmOption<u64>>() == 8);
    assert!(size_of::<ShmOption<()>>() == 1 && ShmOption::<()>::VALUE_OFFSET == 1);
};
