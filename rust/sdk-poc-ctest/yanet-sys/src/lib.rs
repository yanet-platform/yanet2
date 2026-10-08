//! Boundary crate between YANET C shared memory and safe Rust modules.
//!
//! It holds the SDK's `unsafe` code but no packet-processing policy: C layout
//! mirrors ([`ffi`]), strict-provenance resolution of self-relative pointers
//! ([`Root`]), read-only views over validated, published configuration
//! ([`LpmView`], [`decap::DecapConfig`]) and per-round packet handles
//! ([`Packet`]). Every unsafe entry point assumes one environment contract:
//! memory reachable from a published configuration is not written after
//! publication, except for C-owned header bytes (registry items, embedded
//! memory contexts) that this crate never covers with a Rust reference.

#[allow(non_camel_case_types)]
pub mod ffi;

pub mod decap;
mod lpm;
mod packet;
mod resolve;

pub use lpm::{LpmError, LpmView, validate_lpm};
pub use packet::{DecapError, Front, Packet};
pub use resolve::Root;

mod sealed {
    /// Restricts layout implementations to this crate.
    pub trait Sealed {}
}

/// A configuration layout a module can attach to.
///
/// Sealed: attaching projects raw C memory, so only this crate implements it.
pub trait ConfigLayout: sealed::Sealed {
    /// Module type whose configurations have this layout.
    ///
    /// The dataplane hands a module the configurations of its own type, so
    /// exporting a module under any other name would attach this layout to
    /// foreign memory; the export macro rejects that at compile time.
    const TYPE_NAME: &'static str;

    /// Read-only view of one published generation.
    type View<'g>;

    /// Builds the view of the configuration whose header `root` points at.
    ///
    /// # Safety
    ///
    /// `root` must satisfy the [`Root::from_raw`] contract for a
    /// configuration of this layout that was validated before publication and
    /// stays published for `'g`.
    unsafe fn attach<'g>(root: Root<'g>) -> Self::View<'g>;
}
