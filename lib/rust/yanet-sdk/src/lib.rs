//! Allocation-free module storage and dataplane API.
//!
//! A module crate uses `#![no_std]` and `#![forbid(unsafe_code)]`.
//! Shared-memory records derive [`ShmLayout`], which generates a named
//! initializer for direct construction. Handlers receive [`Cx`] and [`Batch`].
//! Neither construction nor a dataplane plugin needs a global allocator.

#![no_std]
#![forbid(unsafe_code)]

/// Packet header views, the `yanet-packet` crate.
pub use yanet_packet as packet;
pub use yanet_sdk_derive::{FfiRecord, ShmLayout, api};
pub use yanet_shm::{
    rel::{ShmBox, ShmSlice},
    resolve::Resolve,
    shm::ShmLayout,
};
pub use yanet_sys::{
    classify,
    common::lpm::{Lpm4, Lpm6, LpmRange},
    cp::{
        BuildError, Built, DeviceIndex, DeviceMap, Encode as Init, FfiRecord, Refusal, boxed, build_config, counter,
        counter_pair, device, lpm, slice, with_devices,
    },
    dp::{
        Batch, CommitCx, Counter, CounterPair, CounterRef, Cx, Device, DeviceRef, DscpMark, Module, Packet, PacketView,
        ResizeError, TunnelError, Verdict,
    },
};

/// Everything a module crate usually imports.
pub mod prelude {
    pub use crate::{
        Batch, Counter, CounterPair, CounterRef, Cx, Device, Module, Packet, ShmBox, ShmLayout, ShmSlice, Verdict,
    };
}

/// Paths the derive expands to; not part of the API.
#[doc(hidden)]
pub mod __private {
    pub use yanet_shm::shm::{MAX_ALIGN, ShmLayout};
    pub use yanet_sys::{
        bindings::net4,
        cp::{BuildError, Encode, FfiRecord, MemoryContext, Owner, Stored, Writer},
    };
}

// Test-only dependencies are visible to the library's own test target.
#[cfg(test)]
use yanet_sdk_testing as _;

/// Dependency names in unsafe expansions cannot be replaced by caller aliases.
#[cfg(doctest)]
mod derive_paths {
    /// A caller's replacement alignment constant cannot weaken the real bound.
    ///
    /// ```compile_fail,E0080
    /// #![forbid(unsafe_code)]
    /// extern crate self as yanet_sdk;
    /// extern crate yanet_sdk as sdk_real;
    /// pub mod __private {
    ///     pub use sdk_real::__private::{ShmLayout, Owner, MemoryContext, Stored};
    ///     pub const MAX_ALIGN: usize = 128;
    /// }
    /// #[derive(sdk_real::ShmLayout)]
    /// #[repr(C, align(128))]
    /// struct Wide { words: [u64; 16] }
    /// ```
    struct AlignmentCannotBeSpoofed;

    /// An accepted alignment is checked against the real dependency.
    ///
    /// ```
    /// #![forbid(unsafe_code)]
    /// extern crate self as yanet_sdk;
    /// extern crate yanet_sdk as sdk_real;
    /// #[derive(sdk_real::ShmLayout)]
    /// #[repr(C, align(64))]
    /// struct Wide {
    ///     words: [u64; 8],
    /// }
    /// ```
    struct AcceptedAlignment;

    /// Recursive storage fields resolve Self to the storage record.
    ///
    /// ```
    /// #![forbid(unsafe_code)]
    /// use yanet_sdk::{ShmLayout, ShmSlice};
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// pub struct Node {
    ///     pub children: ShmSlice<Self>,
    /// }
    /// ```
    struct RecursiveSelf;
}

/// Initializers enforce complete records and exact field types.
#[cfg(doctest)]
mod initializer_contracts {
    /// Every declared field is required in the initializer literal.
    ///
    /// ```compile_fail,E0063
    /// #![forbid(unsafe_code)]
    /// use yanet_sdk::ShmLayout;
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config { value: u64, flags: u64 }
    /// let _ = ConfigInit { value: 1u64 };
    /// ```
    struct MissingField;

    /// A complete literal needs no handwritten input record.
    ///
    /// ```
    /// #![forbid(unsafe_code)]
    /// use yanet_sdk::ShmLayout;
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     value: u64,
    ///     flags: u64,
    /// }
    /// let _ = ConfigInit { value: 1u64, flags: 2u64 };
    /// ```
    struct CompleteFields;

    /// Encoded field widths must match storage exactly.
    ///
    /// ```compile_fail,E0271
    /// #![forbid(unsafe_code)]
    /// use core::convert::Infallible;
    /// use yanet_sdk::{BuildError, ShmLayout};
    /// use yanet_sdk::Init;
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config { value: u64 }
    /// fn accepts(_: impl Init<BuildError<Infallible>, Output = Config>) {}
    /// accepts(ConfigInit { value: 1u32 });
    /// ```
    struct WrongFieldType;

    /// Matching scalar encoders satisfy the exact storage bounds.
    ///
    /// ```
    /// #![forbid(unsafe_code)]
    /// use core::convert::Infallible;
    ///
    /// use yanet_sdk::{BuildError, Init, ShmLayout};
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     value: u64,
    /// }
    /// fn accepts(_: impl Init<BuildError<Infallible>, Output = Config>) {}
    /// accepts(ConfigInit { value: 1u64 });
    /// ```
    struct MatchingFieldType;

    /// Numbers cannot forge resource indices with the same representation.
    ///
    /// ```compile_fail,E0271
    /// #![forbid(unsafe_code)]
    /// use core::convert::Infallible;
    /// use yanet_sdk::{BuildError, Counter, ShmLayout};
    /// use yanet_sdk::Init;
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config { counter: Counter }
    /// fn accepts(_: impl Init<BuildError<Infallible>, Output = Config>) {}
    /// accepts(ConfigInit { counter: 0u64 });
    /// ```
    struct ForgedResource;

    /// A named resource is resolved by the owner during construction.
    ///
    /// ```
    /// #![forbid(unsafe_code)]
    /// use core::convert::Infallible;
    ///
    /// use yanet_sdk::{BuildError, Counter, Init, ShmLayout, counter};
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     counter: Counter,
    /// }
    /// fn accepts(_: impl Init<BuildError<Infallible>, Output = Config>) {}
    /// accepts(ConfigInit { counter: counter("packets") });
    /// ```
    struct NamedResource;

    /// Generated parameters cannot capture authored storage types.
    ///
    /// ```
    /// #![forbid(unsafe_code)]
    /// use core::convert::Infallible;
    ///
    /// use yanet_sdk::{BuildError, Init, ShmLayout};
    /// type __YanetField0 = u64;
    /// type __YanetOwner = u64;
    /// type __YanetError = u64;
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     first: __YanetField0,
    ///     second: __YanetOwner,
    ///     third: __YanetError,
    /// }
    /// fn accepts(_: impl Init<BuildError<Infallible>, Output = Config>) {}
    /// accepts(ConfigInit {
    ///     first: 1u64,
    ///     second: 2u64,
    ///     third: 3u64,
    /// });
    /// ```
    struct AuthoredTypeNames;

    /// An initializer cannot outlive the values borrowed by its callback.
    ///
    /// ```compile_fail,E0515
    /// use yanet_sdk::{ShmSlice, slice};
    /// use yanet_sdk::Init;
    /// fn dangling() -> impl Init<(), Output = ShmSlice<u64>> {
    ///     let values = [1u64, 2, 3];
    ///     slice(values.len(), |index| Ok(values[index]))
    /// }
    /// ```
    struct BorrowCannotEscape;

    /// A callback cannot erase captured values' thread affinity.
    ///
    /// ```compile_fail,E0277
    /// use std::rc::Rc;
    /// use yanet_sdk::slice;
    /// fn send_sync(_: impl Send + Sync) {}
    /// let value = Rc::new(1u64);
    /// send_sync(slice(1, move |_| Ok::<_, ()>(*value)));
    /// ```
    struct ThreadAffinity;

    /// Thread-safe captures preserve the callback's ordinary auto traits.
    ///
    /// ```
    /// use yanet_sdk::slice;
    /// fn send_sync(_: impl Send + Sync) {}
    /// let value = 1u64;
    /// send_sync(slice(1, |_| Ok::<_, ()>(value)));
    /// ```
    struct ThreadSafeCapture;
}
