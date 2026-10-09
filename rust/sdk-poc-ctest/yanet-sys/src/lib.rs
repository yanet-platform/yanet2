//! Boundary crate between YANET C shared memory and safe Rust modules.
//!
//! It holds the SDK's `unsafe` code but no packet-processing policy: C layout
//! mirrors ([`ffi`]), strict-provenance resolution of self-relative pointers
//! ([`Root`]), a generic module configuration whose body the module mirrors
//! ([`ConfigView`]), read-only LPM views ([`LpmView`]) and per-round packet
//! handles ([`Packet`]); it names no module. Every unsafe entry point assumes
//! one environment contract: memory reachable from a published configuration
//! is not written after publication, except for C-owned bytes (the module
//! header, embedded memory contexts) that the mirrors mark `Opaque`.

#[allow(non_camel_case_types)]
pub mod ffi;

mod config;
mod lpm;
mod packet;
mod resolve;

pub use config::{ConfigView, ModuleConfig, body_offset, config_range};
pub use ffi::lpm as Lpm;
pub use lpm::{LpmError, LpmView, validate_lpm};
pub use packet::{DecapError, Front, Packet};
pub use resolve::Root;

/// Shared test layout of the C-built fixture image.
#[cfg(test)]
mod test_body {
    use zerocopy::{FromBytes, KnownLayout};

    use crate::Lpm;

    /// Body of the fixture: two trees after the module header, IPv4 first.
    #[derive(FromBytes, KnownLayout)]
    #[repr(C)]
    pub struct TwoTrees {
        pub first: Lpm,
        pub second: Lpm,
    }
}
