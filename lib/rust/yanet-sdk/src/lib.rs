//! Module-crate surface of the Rust module SDK.
//!
//! A module is one `impl Module` in a `#![no_std]`, `#![forbid(unsafe_code)]`
//! crate: shared-memory types derive [`ShmLayout`], the handler receives the
//! invocation context [`Cx`] and the packet [`Batch`], and edges of the
//! configuration resolve through the context. The control plane builds a
//! configuration in place: the derive generates the `<Type>Parts` struct that a
//! build closure returns, and the scope the closure receives allocates the
//! owned blocks and registers counters and devices. A device is registered once
//! and the [`Registered`] it returns is copied into every entry that uses it.
//! The parts of a table are the author's ranges, borrowed for the build:
//!
//! ```
//! use yanet_sdk::{Device, Lpm4, LpmRange, ShmBox, ShmLayout, ShmSlice};
//!
//! #[derive(ShmLayout)]
//! #[repr(C)]
//! struct Entry {
//!     device: Device,
//!     weight: u64,
//! }
//!
//! #[derive(ShmLayout)]
//! #[repr(C)]
//! struct Config {
//!     default: ShmBox<Entry>,
//!     entries: ShmSlice<Entry>,
//!     routes: Lpm4,
//! }
//!
//! fn config<'b, W: yanet_sdk::Owner, E: From<yanet_sdk::BuildError<W::Error>>>(
//!     scope: yanet_sdk::Scope<'b, '_, W, E>,
//!     ranges: &'b [LpmRange<4>],
//! ) -> Result<ConfigParts<'b, W>, E> {
//!     let eth0 = scope.device("eth0")?;
//!     Ok(ConfigParts {
//!         default: scope.boxed(EntryParts { device: eth0, weight: 0 })?,
//!         entries: scope.slice(3, |i| Ok(EntryParts { device: eth0, weight: [1, 2, 3][i] }))?,
//!         routes: ranges,
//!     })
//! }
//! ```

#![no_std]
#![forbid(unsafe_code)]

/// Packet header views, the `yanet-packet` crate.
pub use yanet_packet as packet;
pub use yanet_sdk_derive::ShmLayout;
pub use yanet_shm::{
    rel::{ShmBox, ShmSlice},
    resolve::Resolve,
    shm::ShmLayout,
};
pub use yanet_sys::{
    common::lpm::{Lpm4, Lpm6, LpmRange},
    cp::{
        BlockAddr, Build, BuildError, Builder, Built, Context, Header, Owned, OwnedSlice, Owner, Refusal, Registered,
        Scope,
    },
    dp::{
        Batch, CommitCx, Counter, CounterRef, Cx, Device, DeviceRef, DscpMark, Module, Packet, PacketView, ResizeError,
        TunnelError, Verdict,
    },
};

/// Everything a module crate usually imports.
pub mod prelude {
    pub use crate::{Batch, Counter, CounterRef, Cx, Device, Module, Packet, ShmBox, ShmLayout, ShmSlice, Verdict};
}

/// Paths the derive expands to; not part of the API.
#[doc(hidden)]
pub mod __private {
    pub use yanet_shm::shm::{MAX_ALIGN, ShmLayout};
    pub use yanet_sys::cp::{Build, MemoryContext, Owner, Partial, Refusal};
}

// Test-only dependencies are visible to the library's own test target.
#[cfg(test)]
use yanet_sdk_testing as _;

/// Doc tests that pin what a build refuses, with the compile error each
/// refusal produces, and the accepted twin of the ranges case.
#[cfg(doctest)]
mod doctests {

    /// A block belongs to the build that allocated it; another build refuses
    /// it.
    ///
    /// ```compile_fail
    /// # use yanet_sdk::{prelude::*, BuildError, Builder};
    /// # use yanet_sdk_testing::cp::{TestError, TestOwner};
    /// # struct Outer;
    /// # impl Module for Outer {
    /// #     const NAME: &'static str = "outer";
    /// #     type Config = u64;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &yanet_sdk::CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # struct Inner;
    /// # impl Module for Inner {
    /// #     const NAME: &'static str = "inner";
    /// #     type Config = ShmBox<u64>;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &yanet_sdk::CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # fn main() {
    /// let owner = TestOwner::new();
    /// Builder::<Outer>::build::<BuildError<TestError>, _>(&owner, "outer", |outer| {
    ///     let block = outer.boxed::<u64>(1)?;
    ///     Builder::<Inner>::build::<BuildError<TestError>, _>(&owner, "inner", |_| Ok(block))?;
    ///     Ok(0)
    /// })
    /// .unwrap();
    /// # }
    /// ```
    struct BlockOfAnotherBuildRefused;

    /// A block cannot outlive the build that allocated it.
    ///
    /// ```compile_fail,E0521
    /// # use yanet_sdk::{prelude::*, BuildError, Builder};
    /// # use yanet_sdk_testing::cp::{TestError, TestOwner};
    /// # struct Mine;
    /// # impl Module for Mine {
    /// #     const NAME: &'static str = "mine";
    /// #     type Config = u64;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &yanet_sdk::CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # fn main() {
    /// let owner = TestOwner::new();
    /// let mut leaked = None;
    /// Builder::<Mine>::build::<BuildError<TestError>, _>(&owner, "mine", |scope| {
    ///     leaked = Some(scope.boxed::<u64>(1)?);
    ///     Ok(0)
    /// })
    /// .unwrap();
    /// # }
    /// ```
    struct BlockOutlivingBuildRefused;

    /// Ranges made inside the closure die with it, so a table refuses them.
    ///
    /// ```compile_fail,E0515
    /// # use yanet_sdk::{prelude::*, BuildError, Builder, Lpm4, LpmRange};
    /// # use yanet_sdk_testing::cp::{TestError, TestOwner};
    /// # struct Mine;
    /// # impl Module for Mine {
    /// #     const NAME: &'static str = "mine";
    /// #     type Config = Lpm4;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &yanet_sdk::CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # fn main() {
    /// let owner = TestOwner::new();
    /// Builder::<Mine>::build::<BuildError<TestError>, _>(&owner, "mine", |_| {
    ///     let ranges = [LpmRange { from: [0; 4], to: [255; 4], value: 1 }];
    ///     Ok(&ranges[..])
    /// })
    /// .unwrap();
    /// # }
    /// ```
    struct RangesMadeInBuildRefused;

    /// Ranges made before the build outlive it, so a table accepts them.
    ///
    /// ```
    /// # use yanet_sdk::{prelude::*, BuildError, Builder, Lpm4, LpmRange};
    /// # use yanet_sdk_testing::cp::{TestError, TestOwner};
    /// # struct Mine;
    /// # impl Module for Mine {
    /// #     const NAME: &'static str = "mine";
    /// #     type Config = Lpm4;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &yanet_sdk::CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # fn main() {
    /// let owner = TestOwner::new();
    /// let ranges = [LpmRange { from: [0; 4], to: [255; 4], value: 1 }];
    /// Builder::<Mine>::build::<BuildError<TestError>, _>(&owner, "mine", |_| Ok(&ranges[..]))
    ///     .unwrap();
    /// # }
    /// ```
    struct RangesMadeBeforeBuildAccepted;

    /// A block of a zero-sized type is refused at compile time, as C has none.
    ///
    /// ```compile_fail,E0080
    /// # use yanet_sdk::{prelude::*, BuildError, Builder};
    /// # use yanet_sdk_testing::cp::{TestError, TestOwner};
    /// # struct Mine;
    /// # impl Module for Mine {
    /// #     const NAME: &'static str = "mine";
    /// #     type Config = ShmBox<()>;
    /// #     type Prepared<'p> = ();
    /// #     fn prepare(_: &yanet_sdk::CommitCx<'_, Self>) {}
    /// #     fn handle<'id>(_: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    /// #         batch.dispatch(|_| Verdict::Pass);
    /// #     }
    /// # }
    /// # fn main() {
    /// let owner = TestOwner::new();
    /// Builder::<Mine>::build::<BuildError<TestError>, _>(&owner, "mine", |scope| scope.boxed::<()>(())).unwrap();
    /// # }
    /// ```
    struct ZeroSizedBlockRefused;

    /// A build runs on one thread, so its blocks never cross to another.
    ///
    /// ```compile_fail,E0277
    /// use yanet_sdk::{Owned, Owner};
    ///
    /// fn sendable<T: Send>() {}
    ///
    /// fn owned_on_any_thread<W: Owner + Sync>() {
    ///     sendable::<Owned<'static, u64, W>>();
    /// }
    /// ```
    struct OwnedAcrossThreadsRefused;
}
