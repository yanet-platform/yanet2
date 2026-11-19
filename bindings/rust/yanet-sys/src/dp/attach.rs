//! Narrow references established at the dataplane callback boundary.
//!
//! Keep the incoming mapping provenance separate from references to individual
//! structures. A shared execution context freezes its own bytes, not the
//! separately allocated buffers and scheduling state its raw pointers address.

use core::{marker::PhantomData, mem::size_of, ptr::NonNull, slice};

use yanet_shm::{resolve::Graph, root::ShmRoot};

use crate::{
    bindings,
    counters::{counter_get_address, counter_value_handles},
    dp::{
        fields::{Counter, CounterLayout, ObjectCounter, ObjectLink, TaggedCounter},
        module::{Module, ModuleConfig},
        object::{Object, ObjectConfig},
    },
};

#[repr(transparent)]
pub(super) struct ModuleEctx(bindings::module_ectx);

#[repr(transparent)]
pub(super) struct ObjectEctx(bindings::object_ectx);

#[repr(transparent)]
pub(super) struct PacketFront(pub(super) bindings::packet_front);

#[repr(transparent)]
pub(super) struct DeviceTarget(bindings::module_device_target);

/// The live call's worker, never borrowed as a whole. Only its stable fields
/// are read, on demand. Other fields remain C's mutable state.
#[derive(Clone, Copy)]
pub(super) struct WorkerRef<'id> {
    raw: NonNull<bindings::dp_worker>,
    _brand: PhantomData<fn(&'id ()) -> &'id ()>,
}

impl WorkerRef<'_> {
    #[inline(always)]
    pub fn as_ptr(self) -> *mut bindings::dp_worker {
        self.raw.as_ptr()
    }

    #[inline(always)]
    pub fn idx(self) -> u64 {
        // SAFETY: attach: call. The brand bounds the live worker's lifetime,
        // and C does not change its index during a synchronous callback.
        unsafe { (*self.as_ptr()).idx }
    }

    #[inline(always)]
    pub fn now(self) -> u64 {
        // SAFETY: attach: call. The round time is stable for the branded call.
        // Never create a reference covering the worker's mutable fields.
        unsafe { (*self.as_ptr()).current_time }
    }
}

pub(super) struct ModuleCall<'id, M: Module> {
    pub graph: Graph<'id>,
    pub ectx: &'id ModuleEctx,
    pub front: &'id mut PacketFront,
    pub worker: WorkerRef<'id>,
    pub config: &'id M::Config,
    pub prepared: &'id M::Prepared<'id>,
}

impl<'id, M: Module> ModuleCall<'id, M> {
    /// # Safety
    /// The arguments are those of this module's running handler (attach:
    /// call, binding, frozen, publish, reclaim). The ectx pointer has raw
    /// mapping-wide provenance, never derived from a Rust reference. The
    /// front and its packets are exclusive to this call. No reassignment or
    /// reclamation overlaps the callback. The lifetime is bounded by it.
    #[inline(always)]
    pub unsafe fn attach(
        worker: *mut bindings::dp_worker,
        ectx: *mut bindings::module_ectx,
        front: *mut bindings::packet_front,
    ) -> Self {
        // SAFETY: attach: mapping, call. Capture raw provenance before
        // borrowing.
        let root = ShmRoot::from_mapping(unsafe { NonNull::new_unchecked(ectx) }.cast());
        // SAFETY: attach: mapping, frozen, reclaim. The callback bounds the
        // brand.
        let graph = unsafe { Graph::new(root) };
        // SAFETY: attach: call, frozen. Only these ectx bytes are borrowed.
        let ectx = unsafe { &*ectx.cast::<ModuleEctx>() };
        // SAFETY: attach: call, packet. The front is initialized and exclusive.
        let front = unsafe { &mut *front.cast::<PacketFront>() };
        let worker = WorkerRef {
            // SAFETY: attach: call. The worker is non-null and live for the brand.
            raw: unsafe { NonNull::new_unchecked(worker) },
            _brand: PhantomData,
        };
        let ModuleEctx(context) = ectx;
        // SAFETY: attach: binding, frozen. The opaque header and frozen body
        // belong to M. Derive from the raw root, not the narrow ectx reference.
        let config = unsafe { (*root.ptr::<ModuleConfig<M::Config>>(context.abs_cp_module.addr())).body() };
        let prepared: *mut M::Prepared<'id> = if size_of::<M::Prepared<'id>>() == 0 {
            NonNull::dangling().as_ptr()
        } else {
            root.ptr(context.abs_module_prepared.addr())
        };
        debug_assert!(prepared.is_aligned(), "misaligned prepared buffer");
        // SAFETY: attach: publish, align. The commit initialized this worker's
        // buffer, frozen for the call. A ZST needs only an aligned address.
        let prepared = unsafe { &*prepared };
        Self {
            graph,
            ectx,
            front,
            worker,
            config,
            prepared,
        }
    }
}

pub(super) struct ModuleCommit<'id, M: Module> {
    pub graph: Graph<'id>,
    pub ectx: &'id ModuleEctx,
    pub config: &'id M::Config,
    pub out: *mut M::Prepared<'id>,
}

impl<'id, M: Module> ModuleCommit<'id, M> {
    /// # Safety
    /// An allocated-buffer commit of M (attach: call, binding, mapping,
    /// frozen, align). Absolute fields are derived before the callback, the
    /// output allocation is exclusive, and no reassignment overlaps the pass.
    /// The raw root covers the mapping and the lifetime is bounded by the pass.
    #[inline(always)]
    pub unsafe fn attach(ectx: *mut bindings::module_ectx, config: *mut bindings::cp_module) -> Self {
        // SAFETY: attach: mapping, call. Preserve incoming raw provenance
        // first.
        let root = ShmRoot::from_mapping(unsafe { NonNull::new_unchecked(ectx) }.cast());
        // SAFETY: attach: mapping, frozen, reclaim. This brand bounds the pass.
        let graph = unsafe { Graph::new(root) };
        // SAFETY: attach: call, frozen. The output is a separate allocation.
        let ectx = unsafe { &*ectx.cast::<ModuleEctx>() };
        let ModuleEctx(context) = ectx;
        // SAFETY: attach: binding, frozen. Only the config body's shared bytes
        // are exposed, its mutable C header stays opaque.
        let config = unsafe { (*root.ptr::<ModuleConfig<M::Config>>(config.addr())).body() };
        Self {
            graph,
            ectx,
            config,
            out: root.ptr(context.abs_module_prepared.addr()),
        }
    }
}

pub(super) struct ObjectCommit<'id, O: Object> {
    pub graph: Graph<'id>,
    pub ectx: &'id ObjectEctx,
    pub config: &'id O::Config,
    pub out: *mut O::Prepared<'id>,
}

impl<'id, O: Object> ObjectCommit<'id, O> {
    /// # Safety
    /// As for `ModuleCommit`, for an allocated-buffer commit of O. The
    /// context's counter storage and worker index are set before this
    /// callback.
    #[inline(always)]
    pub unsafe fn attach(ectx: *mut bindings::object_ectx, config: *mut bindings::cp_object) -> Self {
        // SAFETY: attach: mapping, call. Preserve incoming raw provenance
        // first.
        let root = ShmRoot::from_mapping(unsafe { NonNull::new_unchecked(ectx) }.cast());
        // SAFETY: attach: mapping, frozen, reclaim. This brand bounds the pass.
        let graph = unsafe { Graph::new(root) };
        // SAFETY: attach: call, frozen. The output is a separate allocation.
        let ectx = unsafe { &*ectx.cast::<ObjectEctx>() };
        let ObjectEctx(context) = ectx;
        // SAFETY: attach: binding, frozen. The config header remains opaque.
        let config = unsafe { (*root.ptr::<ObjectConfig<O::Config>>(config.addr())).body() };
        Self {
            graph,
            ectx,
            config,
            out: root.ptr(context.abs_object_prepared.addr()),
        }
    }
}

/// RSS data reported by the NIC for one handler call.
#[derive(Clone, Copy)]
pub struct Rss<'id> {
    /// Raw key used by the NIC hash function.
    pub hash: &'id [u8],
    /// Redirection entries in NIC order, holding physical RX queue IDs.
    pub reta: &'id [u16],
}

/// Physical port data borrowed for one handler call.
pub struct PhysicalPort<'id> {
    rss: Option<Rss<'id>>,
    assignments: &'id [bindings::dp_worker_assignment],
    rss_queue_count: u64,
    slot: u32,
}

impl<'id> PhysicalPort<'id> {
    /// Number of RSS queues configured across the entire device.
    ///
    /// This count is not an upper bound on physical queue IDs.
    #[inline]
    pub fn rss_queue_count(&self) -> u64 {
        self.rss_queue_count
    }

    /// Raw NIC key and NIC-order redirection table, if RSS state is usable.
    ///
    /// The report does not identify which traffic classes the NIC hashes.
    #[inline]
    pub fn rss(&self) -> Option<Rss<'id>> {
        self.rss
    }

    /// All RX queues this worker polls on this port, in assignment order.
    ///
    /// This is independent of the current packet and of RSS participation.
    #[inline]
    pub fn rx_queues(&self) -> impl Iterator<Item = u32> + use<'id> {
        let slot = self.slot;
        self.assignments
            .iter()
            .filter_map(move |assignment| (assignment.device_id == slot).then_some(assignment.queue_id))
    }
}

impl ModuleEctx {
    #[inline(always)]
    pub fn as_ptr(&self) -> *mut bindings::module_ectx {
        let Self(ectx) = self;
        (&raw const *ectx).cast_mut()
    }

    #[inline(always)]
    pub fn recirc_limit(&self) -> u16 {
        let Self(ectx) = self;
        ectx.packet_recirc_limit
    }

    #[inline]
    pub fn current_device(&self) -> u16 {
        let Self(ectx) = self;
        ectx.registry_device_id
    }

    #[inline]
    pub fn physical_port<'id>(
        &'id self,
        graph: Graph<'id>,
        worker: WorkerRef<'id>,
        device: u16,
    ) -> Option<PhysicalPort<'id>> {
        // SAFETY: attach: call, frozen. The shim reads only the live
        // generation, startup topology and this worker's stable assignments.
        let view = unsafe { bindings::yanet_sys_dp_physical_port(self.as_ptr(), worker.as_ptr(), device) };
        if view.slot == u16::MAX {
            return None;
        }
        let root = graph.root();
        let rss = if view.rss_key.is_null() {
            None
        } else {
            debug_assert!(!view.rss_reta.is_null());
            debug_assert!(view.rss_key_len > 0 && view.rss_reta_size > 0);
            // SAFETY: a valid native report supplies two initialized arrays
            // of these lengths, frozen since startup. Recover their mapping
            // provenance before borrowing them for this call.
            unsafe {
                Some(Rss {
                    hash: slice::from_raw_parts(root.ptr(view.rss_key.addr()), usize::from(view.rss_key_len)),
                    reta: slice::from_raw_parts(root.ptr(view.rss_reta.addr()), usize::from(view.rss_reta_size)),
                })
            }
        };
        let assignments = if view.assignment_count == 0 {
            &[]
        } else {
            debug_assert!(!view.assignments.is_null());
            // SAFETY: C initialized exactly this many assignments before the
            // worker started. They stay live and immutable for this call.
            unsafe { slice::from_raw_parts(root.ptr(view.assignments.addr()), view.assignment_count as usize) }
        };
        Some(PhysicalPort {
            rss,
            assignments,
            rss_queue_count: view.rss_queue_count,
            slot: u32::from(view.slot),
        })
    }

    #[inline(always)]
    pub fn device_targets<'id>(&'id self, graph: Graph<'id>) -> &'id [DeviceTarget] {
        let Self(ectx) = self;
        if ectx.device_target_count == 0 {
            return &[];
        }
        // SAFETY: attach: call, frozen. C derived exactly count targets, whose
        // own bytes are frozen. Their pointed-to scheduling entries are not.
        unsafe {
            slice::from_raw_parts(
                graph.root().ptr(ectx.abs_device_targets.addr()),
                ectx.device_target_count as usize,
            )
        }
    }

    #[inline(always)]
    pub fn linked<'id, O: Object>(&'id self, graph: Graph<'id>, link: &'id ObjectLink<O>) -> LinkedObject<'id, O> {
        LinkedObject { module: self, graph, link }
    }

    #[inline(always)]
    pub fn counters<'id>(&'id self, graph: Graph<'id>) -> CounterStorage<'id> {
        let Self(ectx) = self;
        CounterStorage {
            raw: graph.root().ptr(ectx.abs_counter_storage.addr()),
            graph,
        }
    }

    #[inline(always)]
    pub fn counter<'id, T: CounterLayout>(&'id self, graph: Graph<'id>, counter: &'id Counter<T>) -> &'id T::Storage {
        let id = counter.id();
        // SAFETY: attach: counters. This typed built field supplies the
        // registry's id and initialized worker-local layout.
        unsafe { self.counters(graph).cells::<T>(id) }
    }

    #[inline(always)]
    pub fn tagged_counter<'id, T: CounterLayout>(
        &'id self,
        graph: Graph<'id>,
        counter: &'id TaggedCounter<T>,
    ) -> &'id T::Storage {
        let registry = counter.registry();
        let id = counter.id();
        let Self(ectx) = self;
        debug_assert!(registry < ectx.runtime_counter_storage_count);
        let tables = graph
            .root()
            .ptr::<*mut bindings::counter_storage>(ectx.abs_runtime_counter_storages_base.addr());
        // SAFETY: attach: counters, frozen. The typed built counter supplies
        // its own registry, before storage resolution as in the C path.
        let raw = unsafe { *tables.add(registry as usize) };
        let storage = CounterStorage {
            raw: graph.root().ptr(raw.addr()),
            graph,
        };
        // SAFETY: attach: counters. The field supplies this registry's id and
        // initialized layout, live on this worker for the call or prepare pass.
        unsafe { storage.cells::<T>(id) }
    }

    #[inline(always)]
    pub fn tagged<'id>(&'id self, graph: Graph<'id>, registry: u64) -> Option<CounterStorage<'id>> {
        let Self(ectx) = self;
        if registry >= ectx.runtime_counter_storage_count {
            return None;
        }
        let tables = graph
            .root()
            .ptr::<*mut bindings::counter_storage>(ectx.abs_runtime_counter_storages_base.addr());
        // SAFETY: attach: counters, frozen. The registry is in range. Keep this
        // lazy so empty tagged fields never reference a sentinel table.
        let raw = unsafe { *tables.add(registry as usize) };
        Some(CounterStorage {
            raw: graph.root().ptr(raw.addr()),
            graph,
        })
    }
}

impl ObjectEctx {
    #[inline(always)]
    pub fn worker(&self) -> u64 {
        let Self(ectx) = self;
        ectx.worker_idx
    }

    #[inline(always)]
    pub fn counters<'id>(&'id self, graph: Graph<'id>) -> CounterStorage<'id> {
        let Self(ectx) = self;
        let slot = &raw const ectx.counter_storage;
        // SAFETY: attach: counters, frozen. C stores a relative integer here.
        let offset = unsafe { slot.cast::<isize>().read() };
        debug_assert!(offset != 0, "an object context without a counter storage");
        CounterStorage {
            raw: graph.root().rel(slot.addr(), offset),
            graph,
        }
    }

    #[inline(always)]
    pub fn counter<'id, T: CounterLayout>(
        &'id self,
        graph: Graph<'id>,
        counter: &'id ObjectCounter<T>,
    ) -> &'id T::Storage {
        let id = counter.id();
        // SAFETY: attach: counters. The built field supplies the object's
        // registered id and its initialized worker-local layout.
        unsafe { self.counters(graph).cells::<T>(id) }
    }
}

pub(super) struct LinkedObject<'id, O: Object> {
    module: &'id ModuleEctx,
    graph: Graph<'id>,
    link: &'id ObjectLink<O>,
}

impl<'id, O: Object> LinkedObject<'id, O> {
    #[inline(always)]
    fn ectx(&self) -> &'id ObjectEctx {
        let index = self.link.index() as usize;
        let ModuleEctx(module) = self.module;
        debug_assert!(
            index < module.object_link_count as usize,
            "C installs only resolvable links"
        );
        let links = self
            .graph
            .root()
            .ptr::<bindings::module_object_link_ectx>(module.abs_object_links.addr());
        // SAFETY: attach: binding, frozen. C installs the typed built link
        // within the table. Keep its check debug-only as on the original path.
        let link = unsafe { &*links.add(index) };
        // SAFETY: attach: binding, frozen. C resolved this typed link, and the
        // object's ectx bytes stay frozen through the module callback.
        unsafe { &*self.graph.root().ptr::<ObjectEctx>(link.abs_object_ectx.addr()) }
    }

    #[inline(always)]
    pub fn config(&self) -> &'id O::Config {
        // SAFETY: attach: binding, frozen, reclaim. The typed link established
        // O.
        unsafe {
            (*self.graph.root().ptr::<ObjectConfig<O::Config>>({
                let ObjectEctx(ectx) = self.ectx();
                ectx.abs_cp_object.addr()
            }))
            .body()
        }
    }

    #[inline(always)]
    pub fn prepared(&self) -> &'id O::Prepared<'id> {
        let prepared = if size_of::<O::Prepared<'id>>() == 0 {
            NonNull::dangling().as_ptr()
        } else {
            let ObjectEctx(ectx) = self.ectx();
            let raw = ectx.abs_object_prepared;
            debug_assert!(!raw.is_null(), "no prepared buffer of an object that declares one");
            self.graph.root().ptr(raw.addr())
        };
        // SAFETY: attach: binding, publish, align. The linked object's commit
        // precedes this module's pass. A ZST needs only an aligned address.
        unsafe { &*prepared }
    }
}

impl DeviceTarget {
    #[inline(always)]
    pub fn id(&self) -> u16 {
        let Self(target) = self;
        target.device_id
    }

    #[inline(always)]
    pub fn as_ptr(&self) -> *mut bindings::module_device_target {
        let Self(target) = self;
        (&raw const *target).cast_mut()
    }
}

#[derive(Clone, Copy)]
pub(super) struct CounterStorage<'id> {
    raw: *mut bindings::counter_storage,
    graph: Graph<'id>,
}

impl<'id> CounterStorage<'id> {
    /// # Safety
    /// The id indexes this storage's registry and names `T::VALUES` initialized
    /// words of layout T, live on this worker for the branded call or pass.
    #[inline(always)]
    pub unsafe fn cells<T: CounterLayout>(self, id: u64) -> &'id T::Storage {
        // SAFETY: attach: counters, frozen. The attached storage is live, the
        // caller supplies a registered id and its layout.
        let slot = unsafe { counter_get_address(self.graph.root(), self.raw, id) };
        // SAFETY: the caller supplies the initialized worker-local layout.
        unsafe { counter_cells::<T>(slot) }
    }

    #[inline(always)]
    pub fn handles(self) -> NonNull<isize> {
        // SAFETY: attach: counters, frozen. A nonempty registry's table is
        // live.
        let table = unsafe { counter_value_handles(self.graph.root(), self.raw) };
        // SAFETY: attach: counters. The handle table is an allocated block.
        unsafe { NonNull::new_unchecked(table) }
    }
}

/// Borrows initialized worker-local words through their sealed cell layout.
///
/// # Safety
/// The pointer retains storage provenance and is non-null and aligned, even
/// for an empty layout. Its initialized words stay live for the returned
/// lifetime. Only this worker writes them, with compatible cell/raw aliases.
#[inline(always)]
pub(super) unsafe fn counter_cells<'a, T: CounterLayout>(slot: *mut u64) -> &'a T::Storage {
    const {
        assert!(size_of::<T::Storage>() == T::VALUES * size_of::<u64>());
        assert!(align_of::<T::Storage>() == align_of::<u64>());
    }
    // SAFETY: sealed layouts are transparent cells or recursive arrays. The
    // caller guarantees validity, locality, liveness and compatible aliases.
    unsafe { &*slot.cast::<T::Storage>() }
}

/// Attaches only the batch inputs used by the packet unit fixtures.
///
/// # Safety
/// The front's lists and packets must be valid and exclusively owned.
/// No packet operation may use a missing worker pool or routing generation.
#[cfg(test)]
pub(super) unsafe fn test_batch<'id>(
    front: &'id mut bindings::packet_front,
    ectx: &'id bindings::module_ectx,
    worker: &'id mut bindings::dp_worker,
) -> super::batch::Batch<'id> {
    // SAFETY: transparent wrapper over the exclusively borrowed front.
    let front = unsafe { &mut *(&raw mut *front).cast::<PacketFront>() };
    // SAFETY: transparent wrapper over live, shared, frozen fixture metadata.
    let ectx = unsafe { &*(&raw const *ectx).cast::<ModuleEctx>() };
    super::batch::Batch::new(
        front,
        ectx,
        WorkerRef {
            raw: NonNull::from(worker),
            _brand: PhantomData,
        },
    )
}

#[cfg(all(test, debug_assertions))]
mod tests {
    use yanet_sdk_testing::BlockArena;

    use super::*;
    use crate::dp::ObjectCommitCx;

    struct Linked;

    impl Object for Linked {
        const TYPE: &'static str = "linked";
        type Config = u64;
        type Prepared<'p> = ();

        fn prepare(_: &ObjectCommitCx<'_, Self>) {}
    }

    /// Debug checks reject corrupt C link metadata before following the table.
    #[test]
    #[should_panic(expected = "C installs only resolvable links")]
    fn test_link_beyond_context_count_panics() {
        let arena = BlockArena::new();
        // SAFETY: the live empty arena supplies the raw mapping. No reference
        // into an object is produced because the absent link is rejected first.
        let graph = unsafe { Graph::new(arena.root()) };
        let context = ModuleEctx(bindings::module_ectx::default());
        let index = 0u64;
        const {
            assert!(size_of::<ObjectLink<Linked>>() == size_of::<u64>());
            assert!(align_of::<ObjectLink<Linked>>() == align_of::<u64>());
        }
        // SAFETY: an ObjectLink is its index and a type marker, with no
        // padding.
        let link = unsafe { &*(&raw const index).cast::<ObjectLink<Linked>>() };
        context.linked(graph, link).config();
    }
}
