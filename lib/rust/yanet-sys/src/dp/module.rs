//! The module trait: one impl per module binds its name, configuration,
//! prepared value and handler.

use core::mem::{align_of, size_of};

use yanet_shm::opaque::Opaque;

use crate::{
    bindings,
    cp::Build,
    dp::{
        batch::Batch,
        cx::{CommitCx, Cx},
    },
};

/// A dataplane module written in Rust.
///
/// One impl feeds both the generated plugin and the module's api crate, so
/// the body the control plane builds and the body the handler reads are one
/// type.
pub trait Module: Sized + 'static {
    /// Module type name, as the dataplane registers the module.
    const NAME: &'static str;

    /// Configuration body, frozen for a generation and shared by workers; the
    /// control plane builds it with [`Build`].
    type Config: Build + Sync;

    /// Per-worker value of each placement and generation, made by the
    /// prepare step; the unit type when unused.
    ///
    /// Holds plain values and the handles only the prepare context makes,
    /// which carry the lifetime `'p`, so none outlives the context that made
    /// it. It is made on the assigner thread and read on the worker, so it is
    /// `Send`; a value that may not change threads is refused:
    ///
    /// ```compile_fail,E0277
    /// use core::marker::PhantomData;
    /// use yanet_sys::dp::{Batch, CommitCx, Cx, Module, Verdict};
    /// struct Mine;
    /// impl Module for Mine {
    ///     const NAME: &'static str = "mine";
    ///     type Config = u64;
    ///     type Prepared<'p> = PhantomData<*const ()>;
    ///     fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
    ///         batch.dispatch(|_| Verdict::Pass);
    ///     }
    ///     fn prepare(_: &CommitCx<'_, Self>) -> PhantomData<*const ()> {
    ///         PhantomData
    ///     }
    /// }
    /// ```
    ///
    /// It needs no drop and must not be aligned beyond what the block
    /// allocator guarantees; `export_module!` fails the build otherwise:
    ///
    /// ```compile_fail,E0080
    /// use yanet_sys::dp::{Batch, CommitCx, Cx, Module, Verdict};
    /// #[repr(align(128))]
    /// struct Wide(u64);
    /// struct Mine;
    /// impl Module for Mine {
    ///     const NAME: &'static str = "mine";
    ///     type Config = u64;
    ///     type Prepared<'p> = Wide;
    ///     fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
    ///         batch.dispatch(|_| Verdict::Pass);
    ///     }
    ///     fn prepare(_: &CommitCx<'_, Self>) -> Wide {
    ///         Wide(0)
    ///     }
    /// }
    /// yanet_sys::export_module!(name = mine, module = Mine);
    /// ```
    ///
    /// A prepared value within the allocator's alignment is accepted:
    ///
    /// ```
    /// use yanet_sys::dp::{Batch, CommitCx, Cx, Module, Verdict};
    /// #[repr(align(64))]
    /// struct Line(u64);
    /// struct Mine;
    /// impl Module for Mine {
    ///     const NAME: &'static str = "mine";
    ///     type Config = u64;
    ///     type Prepared<'p> = Line;
    ///     fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
    ///         batch.dispatch(|_| Verdict::Pass);
    ///     }
    ///     fn prepare(_: &CommitCx<'_, Self>) -> Line {
    ///         Line(0)
    ///     }
    /// }
    /// yanet_sys::export_module!(name = mine, module = Mine);
    /// ```
    type Prepared<'p>: Send;

    /// Handles one batch of packets.
    ///
    /// Every input packet is passed, dropped or routed to a device (module:
    /// drain); a packet ends where its verdict puts it.
    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>);

    /// Makes the prepared value from the configuration.
    ///
    /// Runs on the dataplane's assigner thread once per worker context of a
    /// generation, before the context reaches its worker, which may still run
    /// the previous generation; the runtime writes the value into the
    /// context's buffer. A zero-sized prepared value has no buffer, and C
    /// runs no commit hook for a context without one, so for it this is not
    /// called.
    /// It resolves what the handler would otherwise resolve per call, as a C
    /// module's commit handler does, and must not reach a worker's memory:
    /// the handles it makes only hold addresses. A module without a prepared
    /// value returns the unit value.
    fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Self::Prepared<'p>;
}

/// Module configuration block: the C module header, then the module body.
///
/// The C header stays opaque: C changes its registry reference count
/// while workers run the configuration, raising it under the configuration
/// lock and dropping it by a relaxed atomic without the lock when the last
/// reader pinning a retired generation releases it.
#[repr(C)]
pub struct ModuleConfig<B> {
    header: Opaque<bindings::cp_module>,
    body: B,
}

impl<B> ModuleConfig<B> {
    /// Offset of the body inside the block.
    pub const BODY_OFFSET: usize = core::mem::offset_of!(Self, body);

    /// Module body.
    #[inline(always)]
    pub fn body(&self) -> &B {
        &self.body
    }
}

const _: () = assert!(core::mem::offset_of!(ModuleConfig<u64>, header) == 0);
const _: () = assert!(size_of::<Opaque<bindings::cp_module>>() == size_of::<bindings::cp_module>());
const _: () = assert!(align_of::<Opaque<bindings::cp_module>>() == align_of::<bindings::cp_module>());
