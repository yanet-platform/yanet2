//! The module trait: one impl per module binds its name, configuration,
//! prepared value and handler.

use core::mem::{align_of, size_of};

use yanet_shm::{opaque::Opaque, shm::ShmLayout};

use crate::{
    bindings,
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

    /// Configuration body, frozen for a generation and shared by workers.
    type Config: ShmLayout + Sync;

    /// Per-worker value of each placement and generation, made by the
    /// prepare step; the unit type when unused.
    ///
    /// Holds plain values and the handles only the prepare context makes,
    /// which carry the lifetime `'p`, so none outlives the context that made
    /// it. It needs no drop and must not be aligned beyond what the block
    /// allocator guarantees; both fail the build.
    type Prepared<'p>;

    /// Handles one batch of packets.
    ///
    /// As in a C handler, a packet the handler takes ends where its verdict
    /// puts it, and one it does not take stays on the input list.
    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>);

    /// Makes the prepared value from the configuration.
    ///
    /// Runs on the dataplane once per worker context of a generation, before
    /// the context reaches its worker, which may still run the previous
    /// generation; the runtime writes the value into the context's buffer.
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
