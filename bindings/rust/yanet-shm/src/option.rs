//! Optional values embedded in shared-memory graphs.
//!
//! An optional value is inline, so an absent one costs its bytes and no block:
//! a table that allocates when C initialises it, such as an LPM, is never
//! initialised when absent and owns nothing. A Rust `Option` has no layout C
//! or the shared-memory contract can rely on, so the option is a tag word
//! followed by the value.

use core::mem::{offset_of, size_of};

/// Value of type `T` that may be absent: a tag word, then the value.
///
/// The value is held inline and exists only where the tag is set; an absent
/// value is zeroed and owns nothing. Like the owning edges it has private
/// fields, no constructor and no `Copy`/`Clone`: a build writes it in place.
/// The option has no padding only if `T` is aligned to at most 8 and sized a
/// multiple of 8; every reading and writing path rejects another `T` when it
/// is compiled for it, so `cargo check` accepts a configuration holding one
/// and building it fails.
#[repr(C)]
pub struct ShmOption<T> {
    present: u64,
    value: T,
}

impl<T> ShmOption<T> {
    /// Byte offset of the value inside the option.
    ///
    /// Evaluating it fails to compile for a `T` the option would pad: aligned
    /// to more than the tag word or sized other than a multiple of it.
    pub const VALUE_OFFSET: usize = {
        assert!(
            size_of::<Self>() == size_of::<u64>() + size_of::<T>(),
            "ShmOption pads a value aligned to more than 8 or sized other than a multiple of 8"
        );
        offset_of!(Self, value)
    };

    /// The value, or `None` when it is absent.
    #[inline(always)]
    pub fn get(&self) -> Option<&T> {
        // Evaluating the offset rejects a padded option at compile time.
        const { assert!(Self::VALUE_OFFSET == size_of::<u64>()) };
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
    assert!(size_of::<ShmOption<[u64; 3]>>() == 32);
    assert!(size_of::<ShmOption<()>>() == 8);
};
