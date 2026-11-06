//! Optional values embedded in shared-memory graphs.
//!
//! An optional value is inline, so an absent one costs its bytes and no block:
//! a table that allocates when C initialises it, such as an LPM, is never
//! initialised when absent and owns nothing. A Rust `Option` has no layout C
//! or the shared-memory contract can rely on, so the option is a tag word
//! followed by the value.

use core::mem::size_of;

use crate::shm::WordLayout;

/// Value of type `T` that may be absent: a tag word, then the value.
///
/// The value is held inline and exists only where the tag is set; an absent
/// value is zeroed and owns nothing. Like the owning edges it has private
/// fields, no constructor and no `Copy`/`Clone`: a build writes it in place.
/// The value is a [`WordLayout`] type, whole words aligned to at most 8, so the
/// option has no padding; an option of any other value is no layout type.
#[repr(C)]
pub struct ShmOption<T> {
    present: u64,
    value: T,
}

impl<T: WordLayout> ShmOption<T> {
    /// Byte offset of the value inside the option, right after the tag word.
    pub const VALUE_OFFSET: usize = size_of::<u64>();

    /// The value, or `None` when it is absent.
    #[inline(always)]
    pub fn get(&self) -> Option<&T> {
        (self.present != 0).then_some(&self.value)
    }

    /// Reports whether the value is present.
    #[inline(always)]
    pub fn is_some(&self) -> bool {
        self.get().is_some()
    }
}

const _: () = {
    assert!(size_of::<ShmOption<u64>>() == 16);
    assert!(core::mem::offset_of!(ShmOption<u64>, value) == ShmOption::<u64>::VALUE_OFFSET);
    assert!(size_of::<ShmOption<[u64; 3]>>() == 32);
    assert!(size_of::<ShmOption<()>>() == 8);
};
