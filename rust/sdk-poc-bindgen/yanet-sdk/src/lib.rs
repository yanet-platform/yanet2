//! Safe surface of the YANET2 Rust module SDK.
//!
//! Re-exports what a module needs from the sys crate, the audited layout
//! derive, and safe Rust ports of hot-path helpers. This crate contains no
//! `unsafe` code.

#![forbid(unsafe_code)]

pub use yanet_sdk_derive::ShmLayout;
pub use yanet_sys::{
    lpm::{Lpm, Lpm4, Lpm6},
    rel::{MapResolver, RelPtr, RelRef, Resolver},
    shm::{self, Module, RelSlice, Shm, ShmLayout},
};
#[cfg(feature = "dp")]
pub use yanet_sys::{
    packet::{DecapError, Packet, PacketFront},
    register_module,
};

/// Configuration body of one published generation, as a handler sees it.
pub type Config<'g, B> = Shm<'g, B, MapResolver<'g>>;

pub mod lpm {
    //! Rust port of the C LPM lookup over the shared-memory LPM.

    use yanet_sys::{
        bindings,
        lpm::{Lpm, LpmEntry},
        rel::Resolver,
        shm::Shm,
    };

    /// Value returned for a key no inserted range covers.
    pub const LPM_VALUE_INVALID: u32 = bindings::LPM_VALUE_INVALID;

    /// Looks a big-endian key up, bit for bit like the C `lpm_lookup`.
    ///
    /// Walks one page per key byte from the root page until a leaf slot.
    /// The value of the last visited slot is returned shifted past the flag,
    /// as C does when the key ends on an intermediate node. An LPM that was
    /// never initialised matches nothing.
    #[inline]
    pub fn lookup<'g, const K: usize, R: Resolver<'g>>(lpm: Shm<'g, Lpm<K>, R>, key: &[u8; K]) -> u32 {
        let Some(mut page) = lpm.root_page() else {
            return LPM_VALUE_INVALID;
        };
        let res = lpm.resolver();
        let Some((&last, inner)) = key.split_last() else {
            return LPM_VALUE_INVALID;
        };
        for &byte in inner {
            match page.values()[usize::from(byte)].entry() {
                LpmEntry::Leaf(value) => return value,
                LpmEntry::Child(child) => page = res.resolve_ref(child),
            }
        }
        (page.values()[usize::from(last)].raw() >> 1) as u32
    }

    /// Reports whether some inserted range covers the key.
    #[inline]
    pub fn contains<'g, const K: usize, R: Resolver<'g>>(lpm: Shm<'g, Lpm<K>, R>, key: &[u8; K]) -> bool {
        lookup(lpm, key) != LPM_VALUE_INVALID
    }
}

// Test-only dependencies are visible to the library's own test target.
#[cfg(test)]
use trybuild as _;
