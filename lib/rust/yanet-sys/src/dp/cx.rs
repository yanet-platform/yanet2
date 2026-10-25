//! Per-invocation contexts of a module.

use core::{marker::PhantomData, ptr::NonNull};

use yanet_shm::{resolve::Resolve, root::ShmRoot};

use crate::{
    bindings,
    counters::counter_get_address,
    dp::{
        batch::DeviceRef,
        fields::{Counter, Device},
        module::Module,
    },
};

/// Device id the dataplane stores for a device absent from the generation:
/// C's `(uint16_t)-1` (`module_ectx_device_target`,
/// `lib/dataplane/pipeline/econtext.h`; the C code has no name for it).
const DEVICE_ABSENT: u16 = u16::MAX;

/// Context of one handler call on one worker.
///
/// Created only by the runtime, one per call; neither `Send` nor `Sync`.
/// It gives the configuration, the prepared value, the worker index and the
/// round time, and resolves counters and devices. References into the
/// configuration carry the invocation brand, not a borrow of the context, so
/// they outlive uses of the context itself.
///
/// A slot of one call's configuration is not resolved through the context of
/// another call:
///
/// ```compile_fail
/// # use yanet_sdk_testing::dp::Model;
/// # use yanet_sys::dp::Cx;
/// # type Mine = Model<yanet_shm::rel::ShmBox<u64>>;
/// fn cross<'a, 'b>(a: &mut Cx<'a, Mine>, b: &mut Cx<'b, Mine>) -> u64 {
///     *a.config().get(b)
/// }
/// ```
///
/// Through its own context it is:
///
/// ```
/// # use yanet_sdk_testing::dp::Model;
/// # use yanet_sys::dp::Cx;
/// # type Mine = Model<yanet_shm::rel::ShmBox<u64>>;
/// fn own<'a>(a: &mut Cx<'a, Mine>) -> u64 {
///     *a.config().get(a)
/// }
/// ```
///
/// The context is not shared with another thread:
///
/// ```compile_fail,E0277
/// # use yanet_sdk_testing::dp::Model;
/// # use yanet_sys::dp::Cx;
/// # type Mine = Model<yanet_shm::rel::ShmBox<u64>>;
/// fn share(cx: &Cx<Mine>) {
///     std::thread::scope(|scope| {
///         scope.spawn(move || cx.now());
///     });
/// }
/// ```
///
/// Only a value read from the context crosses the thread:
///
/// ```
/// # use yanet_sdk_testing::dp::Model;
/// # use yanet_sys::dp::Cx;
/// # type Mine = Model<yanet_shm::rel::ShmBox<u64>>;
/// fn share(cx: &Cx<Mine>) {
///     let now = cx.now();
///     std::thread::scope(|scope| {
///         scope.spawn(move || now);
///     });
/// }
/// ```
pub struct Cx<'id, M: Module> {
    root: ShmRoot,
    ectx: NonNull<bindings::module_ectx>,
    worker: NonNull<bindings::dp_worker>,
    config: &'id M::Config,
    prepared: NonNull<M::Prepared<'id>>,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'id ()) -> &'id ()>,
    // Neither Send nor Sync on purpose, independent of the pointer fields.
    _local: PhantomData<*mut ()>,
}

/// A counter's value of the context's worker, resolved once at commit.
///
/// Holds the address of the value, as a C module holds the pointer
/// `counter_get_address` returned. `Send`, so a prepared value holding it can
/// travel from the assigner thread to its worker, but it does not write:
/// the handler binds it to its context and adds through the bound view,
/// which stays on the worker. Binding is free: the view is the same address.
///
/// A handle does not outlive the prepare context that made it:
///
/// ```compile_fail
/// # use yanet_sdk_testing::dp::Model;
/// # use yanet_sys::dp::{CommitCx, Counter, CounterRef};
/// # type Mine = Model<Counter>;
/// fn keep<'p>(cx: &CommitCx<'p, Mine>, kept: &mut Option<CounterRef<'static>>) {
///     *kept = Some(cx.config().resolve(cx));
/// }
/// ```
///
/// Kept for the context's lifetime, it is accepted:
///
/// ```
/// # use yanet_sdk_testing::dp::Model;
/// # use yanet_sys::dp::{CommitCx, Counter, CounterRef};
/// # type Mine = Model<Counter>;
/// fn keep<'p>(cx: &CommitCx<'p, Mine>, kept: &mut Option<CounterRef<'p>>) {
///     *kept = Some(cx.config().resolve(cx));
/// }
/// ```
#[derive(Clone, Copy)]
pub struct CounterRef<'p> {
    value: NonNull<u64>,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'p ()) -> &'p ()>,
}

// SAFETY: the handle is an address and writes nothing; only the bound view
// writes, and binding needs the call context of the same invariant brand,
// that of the worker's call whose prepared value holds the handle; the
// context is neither `Send` nor `Sync`, so every write happens on that
// worker in its call.
unsafe impl Send for CounterRef<'_> {}

impl<'id> CounterRef<'id> {
    /// The handle bound to the call's context, to add through.
    ///
    /// Only the call context binds: the prepare context does not, so a
    /// handle never writes on the assigner thread:
    ///
    /// ```compile_fail,E0308
    /// # use yanet_sdk_testing::dp::Model;
    /// # use yanet_sys::dp::{CommitCx, Counter};
    /// # type Mine = Model<Counter>;
    /// fn early<'p>(cx: &CommitCx<'p, Mine>) {
    ///     cx.config().resolve(cx).bind(cx).add(1);
    /// }
    /// ```
    ///
    /// The handler binds the handle of its prepared value:
    ///
    /// ```
    /// use yanet_sys::dp::{Batch, CommitCx, Counter, CounterRef, Cx, Module};
    /// struct Mine;
    /// impl Module for Mine {
    ///     const NAME: &'static str = "mine";
    ///     type Config = Counter;
    ///     type Prepared<'p> = CounterRef<'p>;
    ///     fn handle<'id>(cx: &mut Cx<'id, Self>, _: &mut Batch<'id>) {
    ///         cx.prepared().bind(cx).add(1);
    ///     }
    ///     fn prepare<'p>(cx: &CommitCx<'p, Self>) -> CounterRef<'p> {
    ///         cx.config().resolve(cx)
    ///     }
    /// }
    /// ```
    #[inline(always)]
    pub fn bind<M: Module>(&self, _cx: &Cx<'id, M>) -> BoundCounter<'id> {
        BoundCounter {
            value: self.value,
            _brand: PhantomData,
            _local: PhantomData,
        }
    }
}

/// A counter handle bound to the worker's call: adds to the worker's copy.
///
/// Neither `Send` nor `Sync`: the value is this worker's.
///
/// A bound handle is not sent to another thread:
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::BoundCounter;
/// fn send(hits: BoundCounter<'_>) {
///     std::thread::scope(|scope| {
///         scope.spawn(move || hits.add(1));
///     });
/// }
/// ```
///
/// The unbound handle travels:
///
/// ```
/// # use yanet_sys::dp::CounterRef;
/// fn send(hits: CounterRef<'_>) {
///     std::thread::scope(|scope| {
///         scope.spawn(move || hits);
///     });
/// }
/// ```
#[derive(Clone, Copy)]
pub struct BoundCounter<'id> {
    value: NonNull<u64>,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'id ()) -> &'id ()>,
    // Neither Send nor Sync on purpose, independent of the pointer fields.
    _local: PhantomData<*mut ()>,
}

impl BoundCounter<'_> {
    /// Adds to the worker's copy of the counter.
    ///
    /// A plain add, as the `+=` of a C module: only this worker writes its
    /// copy.
    #[inline(always)]
    pub fn add(&self, value: u64) {
        add_to(self.value.as_ptr(), value);
    }
}

/// Adds to the worker's counter value in place.
#[inline(always)]
fn add_to(slot: *mut u64, value: u64) {
    // SAFETY: the address is the aligned value of this worker's counter
    // storage, written only by this worker and live as long as the
    // generation the address was resolved for (callers' contract). The
    // control plane reads it with memcpy (lib/controlplane/agent/counters.c),
    // as it does for the plain `+=` of a C module.
    unsafe { *slot = (*slot).wrapping_add(value) };
}

impl<'id, M: Module> Cx<'id, M> {
    /// Assembles the context of one call.
    ///
    /// # Safety
    ///
    /// The root must cover the shared mapping; the execution context and
    /// worker must be those of the running call; the configuration must be
    /// the published one of this module, frozen for the call; the prepared
    /// buffer must be the context's, holding the value the commit pass wrote
    /// (or dangling for a zero-sized type), frozen and this worker's alone for
    /// the call.
    #[inline(always)]
    pub(crate) unsafe fn new(
        root: ShmRoot,
        ectx: NonNull<bindings::module_ectx>,
        worker: NonNull<bindings::dp_worker>,
        config: &'id M::Config,
        prepared: NonNull<M::Prepared<'id>>,
    ) -> Self {
        Self {
            root,
            ectx,
            worker,
            config,
            prepared,
            _brand: PhantomData,
            _local: PhantomData,
        }
    }

    /// The module's configuration for this generation.
    #[inline(always)]
    pub fn config(&self) -> &'id M::Config {
        self.config
    }

    /// The worker's prepared value for this placement and generation, as
    /// the module's prepare step made it.
    #[inline(always)]
    pub fn prepared(&self) -> &'id M::Prepared<'id> {
        // SAFETY: the buffer holds the value the commit pass wrote before
        // the worker's first call of the generation (attach: publish),
        // frozen and this worker's alone for the call (constructor
        // contract).
        unsafe { self.prepared.as_ref() }
    }

    /// Index of the worker running the call.
    #[inline(always)]
    pub fn worker(&self) -> u64 {
        // SAFETY: the worker structure is live for the call (attach: call) and
        // its index is fixed at startup.
        unsafe { (*self.worker.as_ptr()).idx }
    }

    /// Time of the worker's current round, in nanoseconds; the only time
    /// source of a module.
    #[inline(always)]
    pub fn now(&self) -> u64 {
        // SAFETY: the worker structure is live for the call (attach: call) and
        // its time is written only by this worker between rounds.
        unsafe { (*self.worker.as_ptr()).current_time }
    }
}

// SAFETY: the context holds the root of the mapping its published, frozen
// configuration lives in (attach: mapping, frozen, reclaim), and every `'id`
// reference safe code holds comes from that configuration: contexts are
// created only by the runtime and the brand is invariant.
unsafe impl<'id, M: Module> Resolve<'id> for Cx<'id, M> {
    #[inline(always)]
    fn shm_root(&self) -> ShmRoot {
        self.root
    }
}

/// Context of the prepare pass for one worker context.
///
/// Makes the handles a module's prepared value holds: each resolves once,
/// at commit, what a handler would otherwise resolve on every call, as a C
/// module's commit handler stores resolved pointers in its prepared buffer.
/// Neither `Send` nor `Sync`.
pub struct CommitCx<'c, M: Module> {
    root: ShmRoot,
    ectx: NonNull<bindings::module_ectx>,
    config: &'c M::Config,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'c ()) -> &'c ()>,
    // Neither Send nor Sync on purpose, independent of the pointer fields.
    _local: PhantomData<*mut ()>,
}

impl<'c, M: Module> CommitCx<'c, M> {
    /// Assembles the commit context.
    ///
    /// # Safety
    ///
    /// As for the invocation context: the root covers the mapping, the
    /// execution context is the one being committed, with its counter storage
    /// derived, and the configuration is the published one of this module,
    /// frozen for the pass.
    #[inline(always)]
    pub(crate) unsafe fn new(root: ShmRoot, ectx: NonNull<bindings::module_ectx>, config: &'c M::Config) -> Self {
        Self {
            root,
            ectx,
            config,
            _brand: PhantomData,
            _local: PhantomData,
        }
    }

    /// The module's configuration for the generation being committed.
    #[inline(always)]
    pub fn config(&self) -> &'c M::Config {
        self.config
    }
}

// SAFETY: as for the invocation context.
unsafe impl<'c, M: Module> Resolve<'c> for CommitCx<'c, M> {
    #[inline(always)]
    fn shm_root(&self) -> ShmRoot {
        self.root
    }
}

/// Address of the context's worker's value of a counter, as C's
/// `counter_get_address(id, ectx->abs_counter_storage)`.
///
/// # Safety
///
/// The context must have its absolute counter storage address derived and
/// stay frozen while the result is used, and the id must be an index of the
/// module's counter registry the control plane registered it in.
#[inline(always)]
unsafe fn counter_value(root: ShmRoot, ectx: NonNull<bindings::module_ectx>, id: u64) -> *mut u64 {
    // SAFETY: the context is frozen and its counter storage address was
    // derived (caller contract).
    unsafe {
        let abs_storage = (*ectx.as_ptr()).abs_counter_storage;
        let storage = root.ptr::<bindings::counter_storage>(abs_storage.addr());
        counter_get_address(root, storage, id)
    }
}

impl Counter {
    /// Adds to the current worker's copy of the counter, one of a
    /// data-indexed set the handler picks by value; a counter named by a
    /// single field is added to through a handle in the prepared value.
    ///
    /// The same plain add as the handle's.
    #[inline(always)]
    pub fn add<'id, M: Module>(&'id self, cx: &Cx<'id, M>, value: u64) {
        // SAFETY: the context is frozen for the call and its counter storage
        // address was derived before release to the worker (attach: call,
        // frozen, publish); a built counter id indexes the module's counter
        // registry, whose storage of this context holds one aligned value per
        // counter.
        add_to(unsafe { counter_value(cx.root, cx.ectx, self.id()) }, value);
    }

    /// The context's worker's value of the counter, for the prepared value.
    ///
    /// The handle is made with the prepare context, not the invocation context:
    ///
    /// ```compile_fail,E0308
    /// # use yanet_sdk_testing::dp::Model;
    /// # use yanet_sys::dp::{Counter, Cx};
    /// # type Mine = Model<Counter>;
    /// fn wrong<'id>(cx: &Cx<'id, Mine>) {
    ///     let _ = cx.config().resolve(cx);
    /// }
    /// ```
    ///
    /// With the prepare context it is accepted:
    ///
    /// ```
    /// # use yanet_sdk_testing::dp::Model;
    /// # use yanet_sys::dp::{CommitCx, Counter};
    /// # type Mine = Model<Counter>;
    /// fn right<'p>(cx: &CommitCx<'p, Mine>) {
    ///     let _ = cx.config().resolve(cx);
    /// }
    /// ```
    #[inline(always)]
    pub fn resolve<'c, M: Module>(&'c self, cx: &CommitCx<'c, M>) -> CounterRef<'c> {
        // SAFETY: as for adding, for the pass instead of the call.
        let value = unsafe { counter_value(cx.root, cx.ectx, self.id()) };
        CounterRef {
            // SAFETY: the value lies in the counter storage block, not at
            // address zero.
            value: unsafe { NonNull::new_unchecked(value) },
            _brand: PhantomData,
        }
    }
}

impl Device {
    /// Routing target that transmits through the device, `None` when the
    /// device is not part of the running generation.
    #[inline(always)]
    pub fn resolve<'id, M: Module>(&'id self, cx: &Cx<'id, M>) -> Option<DeviceRef<'id>> {
        device_target(cx, self, false)
    }

    /// Routing target that re-enters the input of the device, `None` when
    /// the device is not part of the running generation.
    #[inline(always)]
    pub fn resolve_input<'id, M: Module>(&'id self, cx: &Cx<'id, M>) -> Option<DeviceRef<'id>> {
        device_target(cx, self, true)
    }
}

/// The routing target of a device, as C's `module_ectx_device_target`
/// (`lib/dataplane/pipeline/econtext.h`) finds it.
#[inline(always)]
fn device_target<'id, M: Module>(cx: &Cx<'id, M>, device: &'id Device, input: bool) -> Option<DeviceRef<'id>> {
    let ectx = cx.ectx.as_ptr();
    // SAFETY: the context is frozen for the call (attach: frozen); the target
    // table has as many entries as the context's device target count, at
    // its absolute address.
    unsafe {
        if device.index() >= (*ectx).device_target_count {
            return None;
        }
        let abs_targets = (*ectx).abs_device_targets;
        let table = cx.root.ptr::<bindings::module_device_target>(abs_targets.addr());
        let target = table.add(device.index() as usize);
        if (*target).device_id == DEVICE_ABSENT {
            return None;
        }
        Some(DeviceRef::new(NonNull::new_unchecked(target), input))
    }
}
