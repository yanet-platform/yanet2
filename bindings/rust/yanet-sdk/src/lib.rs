//! Module-crate surface of the Rust module SDK.
//!
//! A module is one `impl Module` in a `#![no_std]`, `#![forbid(unsafe_code)]`
//! crate: shared-memory types derive [`ShmLayout`], the handler receives the
//! invocation context [`Cx`] and the packet [`Batch`], and edges of the
//! configuration resolve through the context. The control plane, in Go,
//! builds a configuration in place through the views generated from the layout
//! description the derive gives. A device is registered once and every entry
//! that uses it holds its index:
//!
//! ```
//! use yanet_sdk::{Device, Lpm4, ShmBox, ShmLayout, ShmSlice};
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
//! ```
//!
//! An exact-match table is a [`HashIndex`] field: the position each key names
//! in an array of the author's. The handler looks keys up through the
//! [`HashIndexRef`] it resolves for its batch:
//!
//! ```
//! use core::net::Ipv4Addr;
//!
//! use yanet_sdk::{Cx, HashIndex, Module, ShmLayout, ShmSlice};
//!
//! #[derive(ShmLayout)]
//! #[repr(C)]
//! struct Config {
//!     hosts: HashIndex<Ipv4Addr>,
//!     weights: ShmSlice<u64>,
//! }
//!
//! fn weight<'id, M: Module<Config = Config>>(cx: &Cx<'id, M>, host: Ipv4Addr) -> Option<u64> {
//!     let config = cx.config();
//!     let position = config.hosts.resolve(cx).get(host)?;
//!     config.weights.as_slice(cx).get(position as usize).copied()
//! }
//! ```
//!
//! State a worker keeps for itself across batches is a [`PerWorker`] field: a
//! slot per worker, built from one value, which
//! the handler reads and writes for its own worker only, through the
//! [`WorkerSlot`] handle its prepare step keeps: a [`PrepareCx`], of a module
//! or an object, makes the handle of its worker's slot, and only the handler's
//! call uses it. The slots live as long as the
//! configuration: one rebuilt on an update starts them again from that value;
//! an object's slots outlive the modules linking it. State too large for one
//! block of [`PerWorker`] slots, which all workers share, such as a flow
//! table, is a [`PerWorkerSlice`] field: a region of values per worker, each
//! its own block, built from one value repeated over a length chosen at build
//! time, which the handler borrows in place through the [`WorkerSlice`] handle
//! its prepare step keeps.
//!
//! State that outlives the configurations, rebuilt whole on every update, is
//! an [`Object`]: built the same way, with counters of the object's own,
//! [`ObjectCounter`] fields its prepare step resolves, published by name
//! before a module links it and released once no generation holds it. A
//! module configuration links it by type and name in an [`ObjectLink`] field,
//! which the handler resolves to the object's configuration and to the value
//! the object's prepare step made for the worker:
//!
//! ```
//! use core::sync::atomic::{AtomicU64, Ordering};
//!
//! use yanet_sdk::{Cx, Module, Object, ObjectCommitCx, ObjectLink, ShmLayout, ShmSlice};
//!
//! #[derive(ShmLayout)]
//! #[repr(C)]
//! struct StateConfig {
//!     slots: ShmSlice<AtomicU64>,
//! }
//!
//! struct State;
//!
//! impl Object for State {
//!     const TYPE: &'static str = "state";
//!     type Config = StateConfig;
//!     type Prepared<'p> = u64;
//!
//!     fn prepare(cx: &ObjectCommitCx<'_, Self>) -> u64 {
//!         cx.config().slots.as_slice(cx).len() as u64
//!     }
//! }
//!
//! #[derive(ShmLayout)]
//! #[repr(C)]
//! struct Config {
//!     state: ObjectLink<State>,
//! }
//!
//! fn touch<'id, M: Module<Config = Config>>(cx: &Cx<'id, M>) {
//!     let link = &cx.config().state;
//!     if let Some(slot) = link.get(cx).slots.as_slice(cx).first() {
//!         slot.fetch_add(*link.prepared(cx), Ordering::Relaxed);
//!     }
//! }
//! ```

#![no_std]
#![forbid(unsafe_code)]

/// Packet header views, the `yanet-packet` crate.
pub use yanet_packet as packet;
pub use yanet_sdk_derive::ShmLayout;
pub use yanet_shm::{
    index::{HashIndexKey, HashIndexRef},
    layout,
    mutex::{Mutex, MutexGuard},
    rel::{ShmBox, ShmSlice},
    resolve::{Graph, Resolve},
    shm::ShmLayout,
};
pub use yanet_sys::{
    classify::{BURST, Decode, Join, LineAttr, Net4Attr, Net6Attr, Node, PortAttr, Rule, Rules, results_min},
    common::{
        index::HashIndex,
        lpm::{Lpm4, Lpm6, LpmKey},
    },
    dp::{
        Batch, BoundCounter, CommitCx, Counter, CounterLayout, CounterRef, Cx, Device, DeviceRef, Gauge, Module,
        ModuleCx, Object, ObjectCommitCx, ObjectConfig, ObjectCounter, ObjectLink, OuterDscp, Packet, PacketMeta,
        PacketView, ParseError, PerWorker, PerWorkerSlice, PhysicalPort, PrepareCx, ResizeError, Rss, TaggedCounter,
        TaggedCounters, TaggedCountersRef, Traffic, TunnelError, Verdict, WorkerSlice, WorkerSlot,
    },
};

/// Everything a module crate usually imports.
pub mod prelude {
    pub use crate::{
        Batch, Counter, CounterRef, Cx, Device, Module, Packet, PerWorker, PerWorkerSlice, PrepareCx, ShmBox,
        ShmLayout, ShmSlice, Traffic, Verdict, WorkerSlice, WorkerSlot,
    };
}

/// Paths the derive expands to; not part of the API.
#[doc(hidden)]
pub mod __private {
    pub use yanet_shm::{
        layout::{Field as LayoutField, Kind as LayoutKind, Node as LayoutNode},
        shm::{MAX_ALIGN, ShmLayout},
    };
}

/// Doc tests that pin what a configuration refuses, with the compile error
/// each refusal produces, and the accepted twin of each.
#[cfg(doctest)]
mod doctests {
    /// A per-worker slot holds plain values only: a slot owning a block is
    /// not `Copy`, so a configuration with one does not build.
    ///
    /// ```compile_fail,E0277
    /// use yanet_sdk::{PerWorker, ShmLayout, ShmSlice};
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     edges: PerWorker<ShmSlice<u64>>,
    /// }
    /// ```
    struct PerWorkerEdgeRefused;

    /// A per-worker slot of plain values builds, the accepted twin of the
    /// refusal above.
    ///
    /// ```
    /// use yanet_sdk::{PerWorker, ShmLayout};
    /// #[derive(Clone, Copy, ShmLayout)]
    /// #[repr(C)]
    /// struct Budget {
    ///     tokens: u64,
    ///     last: u64,
    /// }
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     budgets: PerWorker<Budget>,
    /// }
    /// ```
    struct PerWorkerPlainAccepted;

    /// A per-worker region holds plain values only, as a slot does.
    ///
    /// ```compile_fail,E0277
    /// use yanet_sdk::{PerWorkerSlice, ShmLayout, ShmSlice};
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     edges: PerWorkerSlice<ShmSlice<u64>>,
    /// }
    /// ```
    struct PerWorkerSliceEdgeRefused;

    /// A per-worker region of plain values builds, the accepted twin of the
    /// refusal above.
    ///
    /// ```
    /// use yanet_sdk::{PerWorkerSlice, ShmLayout};
    /// #[derive(Clone, Copy, ShmLayout)]
    /// #[repr(C)]
    /// struct Bucket {
    ///     key: u64,
    ///     seen: u64,
    /// }
    /// #[derive(ShmLayout)]
    /// #[repr(C)]
    /// struct Config {
    ///     buckets: PerWorkerSlice<Bucket>,
    /// }
    /// ```
    struct PerWorkerSlicePlainAccepted;

    /// A caller's replacement alignment limit cannot admit over-aligned
    /// storage.
    ///
    /// ```compile_fail,E0080
    /// #![forbid(unsafe_code)]
    /// extern crate self as yanet_sdk;
    /// extern crate yanet_sdk as sdk_real;
    /// pub mod __private {
    ///     pub use sdk_real::__private::*;
    ///     pub const MAX_ALIGN: usize = 128;
    /// }
    /// #[derive(sdk_real::ShmLayout)]
    /// #[repr(C, align(128))]
    /// struct Wide {
    ///     words: [u64; 16],
    /// }
    /// fn main() {}
    /// ```
    struct CallerAlignmentLimitRefused;

    /// Caller aliases cannot replace the checks applied to valid storage.
    ///
    /// ```
    /// #![forbid(unsafe_code)]
    /// extern crate core as core_real;
    /// extern crate self as core;
    /// extern crate self as yanet_sdk;
    /// extern crate yanet_sdk as sdk_real;
    /// pub use core_real::*;
    /// pub mod mem {
    ///     pub use core_real::mem::*;
    ///     pub const fn align_of<T>() -> usize {
    ///         128
    ///     }
    /// }
    /// pub mod __private {
    ///     pub use sdk_real::__private::*;
    ///     pub const MAX_ALIGN: usize = 0;
    /// }
    /// #[derive(sdk_real::ShmLayout)]
    /// #[repr(C, align(64))]
    /// struct Wide {
    ///     words: [u64; 8],
    /// }
    /// fn main() {}
    /// ```
    struct CallerAliasesKeepValidAlignment;
}
