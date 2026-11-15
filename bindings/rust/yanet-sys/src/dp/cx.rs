//! Per-invocation contexts of a module.

use core::{
    marker::PhantomData,
    mem::size_of,
    ops::{AddAssign, SubAssign},
    panic::{RefUnwindSafe, UnwindSafe},
    ptr::NonNull,
};

use yanet_shm::{resolve::Resolve, root::ShmRoot};

use crate::{
    bindings,
    counters::{counter_get_address, counter_handle_value, counter_value_handles},
    dp::{
        batch::DeviceRef,
        fields::{Counter, CounterLayout, Device, Gauge, ObjectCounter, ObjectLink, TaggedCounter, TaggedCounters},
        module::Module,
        object::{Object, ObjectConfig},
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
/// another call (a lifetime error, which has no code):
///
/// ```compile_fail
/// # use yanet_shm::rel::ShmBox;
/// # use yanet_sys::dp::{Cx, Module};
/// fn cross<'a, 'b, M: Module<Config = ShmBox<u64>>>(a: &mut Cx<'a, M>, b: &mut Cx<'b, M>) -> u64 {
///     *a.config().get(b)
/// }
/// ```
///
/// Through its own context it is:
///
/// ```
/// # use yanet_shm::rel::ShmBox;
/// # use yanet_sys::dp::{Cx, Module};
/// fn own<'a, M: Module<Config = ShmBox<u64>>>(a: &mut Cx<'a, M>) -> u64 {
///     *a.config().get(a)
/// }
/// ```
///
/// The context is not shared with another thread:
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{Cx, Module};
/// fn share<M: Module>(cx: &Cx<M>) {
///     std::thread::scope(|scope| {
///         scope.spawn(move || cx.now());
///     });
/// }
/// ```
///
/// Nor is it sent to another thread:
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{Cx, Module};
/// fn send<M: Module>(cx: Cx<'_, M>) {
///     std::thread::scope(|scope| {
///         scope.spawn(move || cx.now());
///     });
/// }
/// ```
///
/// Only a value read from the context crosses the thread:
///
/// ```
/// # use yanet_sys::dp::{Cx, Module};
/// fn share<M: Module>(cx: &Cx<M>) {
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
    prepared: &'id M::Prepared<'id>,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'id ()) -> &'id ()>,
    // Neither Send nor Sync on purpose, independent of the pointer fields.
    _local: PhantomData<*mut ()>,
}

/// A counter's values of the context's worker, resolved once at commit, of a
/// counter of the layout `T`: a module's [`Counter`] or [`TaggedCounter`], or
/// an [`ObjectCounter`] the object's prepare step resolves.
///
/// Holds the address of the first value, as a C module holds the pointer
/// `counter_get_address` returned. `Send`, so a prepared value holding it can
/// travel from the assigner thread to its worker, but it does not write: the
/// handler binds it to its context and reads and adds through the bound view,
/// which stays on the worker. Binding is free: the view is the same address.
///
/// A handle does not outlive the prepare context that made it (a lifetime
/// error, which has no code):
///
/// ```compile_fail
/// # use yanet_sys::dp::{CommitCx, Counter, CounterRef, Module};
/// fn keep<'p, M: Module<Config = Counter>>(
///     cx: &CommitCx<'p, M>,
///     kept: &mut Option<CounterRef<'static>>,
/// ) {
///     *kept = Some(cx.config().resolve(cx));
/// }
/// ```
///
/// Kept for the context's lifetime, it is accepted:
///
/// ```
/// # use yanet_sys::dp::{CommitCx, Counter, CounterRef, Module};
/// fn keep<'p, M: Module<Config = Counter>>(
///     cx: &CommitCx<'p, M>,
///     kept: &mut Option<CounterRef<'p>>,
/// ) {
///     *kept = Some(cx.config().resolve(cx));
/// }
/// ```
///
/// Prepared handles retain their non-sharing contract for every layout:
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{CounterRef, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<CounterRef<'static, u64>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{CounterRef, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<CounterRef<'static, Traffic>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{CounterRef, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<CounterRef<'static, Gauge>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{CounterRef, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<CounterRef<'static, [[Traffic; 3]; 2]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{CounterRef, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<CounterRef<'static, [u64; 0]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{CounterRef, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<CounterRef<'static, [Traffic; 0]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{CounterRef, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<CounterRef<'static, [[Gauge; 0]; 3]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{CounterRef, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<CounterRef<'static, [[Traffic; 3]; 0]>>();
/// ```
pub struct CounterRef<'p, T = u64> {
    value: NonNull<u64>,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'p ()) -> &'p ()>,
    // The counter's layout, which sets the values it adds to.
    _layout: PhantomData<fn() -> T>,
}

impl<T> Clone for CounterRef<'_, T> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<T> Copy for CounterRef<'_, T> {}

// SAFETY: the handle is an address and writes nothing; only the bound views
// write, and binding needs the call context of the same invariant brand: that
// of the worker's call whose prepared value holds the handle, or that reads it
// from the prepared value of an object the module links, which C made for the
// object's context of the same worker; the context is neither `Send` nor
// `Sync`, so every write happens on that worker in its call.
unsafe impl<T> Send for CounterRef<'_, T> {}

impl<'id, T> CounterRef<'id, T> {
    /// The handle bound to the call's context, to add through.
    ///
    /// Only the call context binds: the prepare context does not, so a
    /// handle never writes on the assigner thread:
    ///
    /// ```compile_fail,E0308
    /// # use yanet_sys::dp::{CommitCx, Counter, Module};
    /// fn early<'p, M: Module<Config = Counter>>(cx: &CommitCx<'p, M>) {
    ///     cx.config().resolve(cx).bind(cx).add(1);
    /// }
    /// ```
    ///
    /// The handler binds the handle of its prepared value:
    ///
    /// ```
    /// use yanet_sys::dp::{Batch, CommitCx, Counter, CounterRef, Cx, Module, Verdict};
    /// struct Mine;
    /// impl Module for Mine {
    ///     const NAME: &'static str = "mine";
    ///     type Config = Counter;
    ///     type Prepared<'p> = CounterRef<'p>;
    ///     fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    ///         let mut hits = cx.prepared().bind(cx);
    ///         hits += 1;
    ///         batch.dispatch(|_| Verdict::Pass);
    ///     }
    ///     fn prepare<'p>(cx: &CommitCx<'p, Self>) -> CounterRef<'p> {
    ///         cx.config().resolve(cx)
    ///     }
    /// }
    /// ```
    #[inline(always)]
    pub fn bind<M: Module>(&self, _cx: &Cx<'id, M>) -> BoundCounter<'id, T>
    where
        T: CounterLayout,
    {
        // SAFETY: TCB counter binding. The prepared pointer retains the mapping
        // provenance and names initialized, aligned values of this worker,
        // live for the same branded call. All overlapping accesses use cells.
        let cells = unsafe { counter_cells::<T>(self.value.as_ptr()) };
        BoundCounter {
            cells,
            _brand: PhantomData,
            _local: PhantomData,
            _layout: PhantomData,
        }
    }
}

/// A counter handle bound to the worker's call: reads the worker's values of
/// the counter and adds to them, and takes from a [`Gauge`].
///
/// Neither `Send` nor `Sync`: the values are this worker's.
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
///
/// Copies share worker-local cells. Arithmetic wraps and multi-value updates
/// are not transactional. Both unwind markers describe plain-word state,
/// not rollback on panic.
///
/// Every layout stays local, including empty arrays:
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Send>() {}
/// requires::<BoundCounter<'static, u64>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<BoundCounter<'static, u64>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Send>() {}
/// requires::<BoundCounter<'static, Traffic>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<BoundCounter<'static, Traffic>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Send>() {}
/// requires::<BoundCounter<'static, Gauge>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<BoundCounter<'static, Gauge>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Send>() {}
/// requires::<BoundCounter<'static, [[Traffic; 3]; 2]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<BoundCounter<'static, [[Traffic; 3]; 2]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Send>() {}
/// requires::<BoundCounter<'static, [u64; 0]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<BoundCounter<'static, [u64; 0]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Send>() {}
/// requires::<BoundCounter<'static, [Traffic; 0]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<BoundCounter<'static, [Traffic; 0]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Send>() {}
/// requires::<BoundCounter<'static, [[Gauge; 0]; 3]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<BoundCounter<'static, [[Gauge; 0]; 3]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Send>() {}
/// requires::<BoundCounter<'static, [[Traffic; 3]; 0]>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{BoundCounter, Gauge, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<BoundCounter<'static, [[Traffic; 3]; 0]>>();
/// ```
///
/// The invariant call brand cannot be shortened:
///
/// ```compile_fail
/// # use yanet_sys::dp::BoundCounter;
/// fn shorten<'a>(view: BoundCounter<'static>) -> BoundCounter<'a> {
///     view
/// }
/// ```
///
/// A bound view cannot escape a call:
///
/// ```compile_fail
/// # use yanet_sys::dp::{CounterRef, Cx, Module, BoundCounter};
/// fn escape<'id, M: Module>(cx: &Cx<'id, M>, handle: CounterRef<'id>) -> BoundCounter<'static> {
///     handle.bind(cx)
/// }
/// ```
pub struct BoundCounter<'id, T: CounterLayout = u64> {
    cells: &'id T::Storage,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'id ()) -> &'id ()>,
    // Neither Send nor Sync on purpose, independent of the pointer fields.
    _local: PhantomData<*mut ()>,
    // The counter's layout, which sets the values it adds to.
    _layout: PhantomData<fn() -> T>,
}

impl<T: CounterLayout> Clone for BoundCounter<'_, T> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<T: CounterLayout> Copy for BoundCounter<'_, T> {}

// Unwinding exposes only plain wrapping words, not borrow guards or poisoned
// invariants. Multi-value updates do not promise transactional snapshots.
impl<T: CounterLayout> UnwindSafe for BoundCounter<'_, T> {}
impl<T: CounterLayout> RefUnwindSafe for BoundCounter<'_, T> {}

impl<T: CounterLayout> BoundCounter<'_, T> {
    /// Adds the value to the worker's copy of the counter.
    ///
    /// A plain add, as the `+=` of a C module: only this worker writes its
    /// copy.
    #[inline(always)]
    pub fn add(&self, value: T::Value) {
        T::add(self.cells, value);
    }

    /// The worker's copy of the counter, which only this worker writes.
    #[inline(always)]
    pub fn get(&self) -> T::Value {
        T::read(self.cells)
    }
}

impl<T> AddAssign<T::Value> for BoundCounter<'_, T>
where
    T: CounterLayout,
{
    #[inline(always)]
    fn add_assign(&mut self, value: T::Value) {
        self.add(value);
    }
}

impl BoundCounter<'_, Gauge> {
    /// Takes the value from the worker's copy of the gauge.
    ///
    /// The plain `-=` of a C module, as an add is its `+=`: only this worker
    /// writes its copy. A worker that takes more than it gave wraps its copy
    /// below zero, and the sum of the workers' copies stays the level.
    ///
    /// A counter of what happened only adds, and takes nothing:
    ///
    /// ```compile_fail,E0599
    /// # use yanet_sys::dp::BoundCounter;
    /// fn undo(hits: BoundCounter<'_>) {
    ///     hits.sub(1);
    /// }
    /// ```
    #[inline(always)]
    pub fn sub(&self, value: u64) {
        self.cells.set(self.cells.get().wrapping_sub(value));
    }
}

impl SubAssign<u64> for BoundCounter<'_, Gauge> {
    #[inline(always)]
    fn sub_assign(&mut self, value: u64) {
        self.sub(value);
    }
}

impl<'id, T, const N: usize> BoundCounter<'id, [T; N]>
where
    T: CounterLayout,
{
    /// The counter's value at the index, bound as a counter of its own
    /// layout.
    ///
    /// The index is a constant, which the build checks: one past the values
    /// fails it.
    ///
    /// ```compile_fail,E0080
    /// # use yanet_sys::dp::{BoundCounter, Gauge};
    /// fn past(levels: BoundCounter<'_, [Gauge; 4]>) {
    ///     levels.at::<4>().add(1);
    /// }
    /// # // The build checks the index only in a function it emits.
    /// # let _: fn(BoundCounter<'_, [Gauge; 4]>) = past;
    /// ```
    ///
    /// The handler picks a value of a counter of several by its index:
    ///
    /// ```
    /// use yanet_sys::dp::{Batch, CommitCx, Counter, CounterRef, Cx, Module, Traffic, Verdict};
    /// const SHORT: usize = 0;
    /// const LONG: usize = 1;
    /// struct Mine;
    /// impl Module for Mine {
    ///     const NAME: &'static str = "mine";
    ///     type Config = Counter<[Traffic; 2]>;
    ///     type Prepared<'p> = CounterRef<'p, [Traffic; 2]>;
    ///     fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    ///         let sizes = cx.prepared().bind(cx);
    ///         batch.dispatch(|packet| {
    ///             let len = packet.data().len() as u64;
    ///             let size = if len < 128 {
    ///                 sizes.at::<SHORT>()
    ///             } else {
    ///                 sizes.at::<LONG>()
    ///             };
    ///             size.add(Traffic::packet(len));
    ///             Verdict::Pass
    ///         });
    ///     }
    ///     fn prepare<'p>(cx: &CommitCx<'p, Self>) -> CounterRef<'p, [Traffic; 2]> {
    ///         cx.config().resolve(cx)
    ///     }
    /// }
    /// ```
    #[inline(always)]
    pub fn at<const I: usize>(&self) -> BoundCounter<'id, T> {
        const { assert!(I < N, "an index past the counter's values") };
        BoundCounter {
            cells: &self.cells[I],
            _brand: PhantomData,
            _local: PhantomData,
            _layout: PhantomData,
        }
    }
}

/// Handle of the context's worker's values of the counters of a
/// [`TaggedCounters`] field, resolved once per generation for the prepared
/// value: the handle table of the registry's storage and the number of
/// counters the configuration registered.
///
/// Like a [`CounterRef`], it is `Send` and does not write: the handler binds it
/// to its context and adds by id through the bound view. Binding is free.
///
/// Only the call context binds: the prepare context does not, so a handle
/// never writes on the assigner thread:
///
/// ```compile_fail,E0308
/// # use yanet_sys::dp::{CommitCx, Module, TaggedCounters};
/// fn early<'p, M: Module<Config = TaggedCounters>>(cx: &CommitCx<'p, M>) {
///     cx.config().resolve(cx).bind(cx).add(0, 1);
/// }
/// ```
///
/// The handler binds the handle of its prepared value and adds by id:
///
/// ```
/// use yanet_sys::dp::{
///     Batch, CommitCx, Cx, Module, TaggedCounters, TaggedCountersRef, Traffic, Verdict,
/// };
/// struct Mine;
/// impl Module for Mine {
///     const NAME: &'static str = "mine";
///     type Config = TaggedCounters<Traffic>;
///     type Prepared<'p> = TaggedCountersRef<'p, Traffic>;
///     fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
///         let counters = cx.prepared().bind(cx);
///         batch.dispatch(|packet| {
///             let id = packet.data().first().copied().unwrap_or_default();
///             counters.add(u64::from(id), Traffic::packet(packet.data().len() as u64));
///             Verdict::Pass
///         });
///     }
///     fn prepare<'p>(cx: &CommitCx<'p, Self>) -> TaggedCountersRef<'p, Traffic> {
///         cx.config().resolve(cx)
///     }
/// }
/// ```
///
/// Prepared tagged handles cannot be shared, even for an empty layout:
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::TaggedCountersRef;
/// fn requires<T: Sync>() {}
/// requires::<TaggedCountersRef<'static>>();
/// ```
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::{TaggedCountersRef, Traffic};
/// fn requires<T: Sync>() {}
/// requires::<TaggedCountersRef<'static, [Traffic; 0]>>();
/// ```
pub struct TaggedCountersRef<'p, T = u64> {
    table: NonNull<isize>,
    len: u64,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'p ()) -> &'p ()>,
    // The counters' layout, which sets the values each adds to.
    _layout: PhantomData<fn() -> T>,
}

impl<T> Clone for TaggedCountersRef<'_, T> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<T> Copy for TaggedCountersRef<'_, T> {}

// SAFETY: the handle is an address and a number and writes nothing; only the
// bound view writes, and binding needs the call context of the same invariant
// brand, that of the worker's call whose prepared value holds the handle; the
// context is neither `Send` nor `Sync`, so every write happens on that worker
// in its call.
unsafe impl<T> Send for TaggedCountersRef<'_, T> {}

impl<'id, T> TaggedCountersRef<'id, T> {
    /// The handle bound to the call's context, to add through.
    #[inline(always)]
    pub fn bind<M>(&self, _cx: &Cx<'id, M>) -> BoundTaggedCounters<'id, T>
    where
        M: Module,
    {
        BoundTaggedCounters {
            table: self.table,
            len: self.len,
            _brand: PhantomData,
            _local: PhantomData,
            _layout: PhantomData,
        }
    }
}

/// A [`TaggedCountersRef`] bound to the worker's call: adds to the worker's
/// copies of the counters by id.
///
/// Neither `Send` nor `Sync`: the values are this worker's.
///
/// A bound handle is not sent to another thread:
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::BoundTaggedCounters;
/// fn send(rules: BoundTaggedCounters<'_>) {
///     std::thread::scope(|scope| {
///         scope.spawn(move || rules.add(0, 1));
///     });
/// }
/// ```
pub struct BoundTaggedCounters<'id, T = u64> {
    table: NonNull<isize>,
    len: u64,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'id ()) -> &'id ()>,
    // Neither Send nor Sync on purpose, independent of the pointer fields.
    _local: PhantomData<*mut ()>,
    // The counters' layout, which sets the values each adds to.
    _layout: PhantomData<fn() -> T>,
}

impl<T> Clone for BoundTaggedCounters<'_, T> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<T> Copy for BoundTaggedCounters<'_, T> {}

impl<T> BoundTaggedCounters<'_, T>
where
    T: CounterLayout,
{
    /// Adds the value to the worker's copy of the counter of the given id; an
    /// id past the counters the configuration registered counts nowhere.
    ///
    /// A plain add, as the `+=` of a C module: only this worker writes its
    /// copy.
    #[inline(always)]
    pub fn add(&self, id: u64, value: T::Value) {
        if id >= self.len {
            return;
        }
        // SAFETY: the table is the handle table of the registry's storage for
        // the generation the call runs, resolved by the bound handle (bound to
        // the call context of the same brand), with the root's provenance; the
        // id is below the counters the configuration registered, which C keeps
        // first in the registry, each of the layout `T`, so the handle names
        // this worker's aligned values of the counter, as many as the layout's.
        let slot = unsafe { counter_handle_value(self.table.as_ptr(), id) };
        // SAFETY: TCB tagged cells. The resolved values are initialized and
        // worker-local, live for this call, and aliased only through cells.
        T::add(unsafe { counter_cells::<T>(slot) }, value);
    }
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
        // SAFETY: the buffer holds the value the commit pass wrote before
        // the worker's first call of the generation (attach: publish),
        // frozen and this worker's alone for the call (constructor
        // contract).
        let prepared = unsafe { prepared.as_ref() };
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
        self.prepared
    }

    /// Index of the worker running the call.
    #[inline(always)]
    pub fn worker(&self) -> u64 {
        // SAFETY: the worker structure is live for the call, and its index
        // does not change during it (attach: call).
        unsafe { (*self.worker.as_ptr()).idx }
    }

    /// Time of the worker's current round, in nanoseconds; the only time
    /// source of a module.
    #[inline(always)]
    pub fn now(&self) -> u64 {
        // SAFETY: the worker structure is live for the call, and its time
        // does not change during it (attach: call).
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

    /// Index of the worker whose context is being committed, `None` when the
    /// context is not among its generation's worker contexts.
    ///
    /// C does not tell a module's commit its worker; the generation keeps one
    /// context per worker at the worker's index, so the index is where the
    /// module's context appears, as C modules find it.
    #[inline(always)]
    fn worker(&self) -> Option<u64> {
        // SAFETY: the context is the one being committed, frozen with its
        // absolute fields derived, and its generation's contexts with it
        // (constructor contract, attach: call, frozen).
        let idx = unsafe { bindings::yanet_sys_dp_module_worker_idx(self.ectx.as_ptr()) };
        (idx != bindings::YANET_SYS_DP_WORKER_IDX_NONE).then_some(idx)
    }
}

// SAFETY: as for the invocation context.
unsafe impl<'c, M: Module> Resolve<'c> for CommitCx<'c, M> {
    #[inline(always)]
    fn shm_root(&self) -> ShmRoot {
        self.root
    }
}

/// Context of the prepare pass for one worker context of an object.
///
/// Gives the object's configuration, whose edges resolve through it, and the
/// context's worker to the object's prepare step, and resolves the object's
/// counters. Neither `Send` nor `Sync`.
pub struct ObjectCommitCx<'c, O: Object> {
    root: ShmRoot,
    ectx: NonNull<bindings::object_ectx>,
    config: &'c O::Config,
    // Invariant brand: values of two different contexts never unify, so they cannot be mixed.
    _brand: PhantomData<fn(&'c ()) -> &'c ()>,
    // Neither Send nor Sync on purpose, independent of the pointer fields.
    _local: PhantomData<*mut ()>,
}

impl<'c, O: Object> ObjectCommitCx<'c, O> {
    /// Assembles the commit context.
    ///
    /// # Safety
    ///
    /// The root covers the mapping, the execution context is the one being
    /// committed, and the configuration is the published one of the object
    /// being committed, frozen for the pass but for its atomics and mutex
    /// payloads.
    #[inline(always)]
    pub(crate) unsafe fn new(root: ShmRoot, ectx: NonNull<bindings::object_ectx>, config: &'c O::Config) -> Self {
        Self {
            root,
            ectx,
            config,
            _brand: PhantomData,
            _local: PhantomData,
        }
    }

    /// The object's configuration in the generation being committed.
    #[inline(always)]
    pub fn config(&self) -> &'c O::Config {
        self.config
    }

    /// Index of the worker whose context is being committed, which C records
    /// in the object's context when it builds it.
    #[inline(always)]
    fn worker(&self) -> u64 {
        // SAFETY: the context is the one being committed, frozen but for its
        // prepared buffer (constructor contract, attach: frozen).
        unsafe { (*self.ectx.as_ptr()).worker_idx }
    }
}

// SAFETY: as for the module's commit context.
unsafe impl<'c, O: Object> Resolve<'c> for ObjectCommitCx<'c, O> {
    #[inline(always)]
    fn shm_root(&self) -> ShmRoot {
        self.root
    }
}

/// A context of a prepare pass, of a module or of an object, which makes the
/// handles of its worker's per-worker slots and regions.
///
/// Sealed: the contexts are [`CommitCx`] and [`ObjectCommitCx`].
pub trait PrepareCx<'p>: Resolve<'p> + sealed::Prepare {}

impl<'p, M: Module> PrepareCx<'p> for CommitCx<'p, M> {}

impl<'p, O: Object> PrepareCx<'p> for ObjectCommitCx<'p, O> {}

/// A context of a module's call or of its prepare pass, either of which
/// resolves the module's [`ObjectLink`]s.
///
/// Sealed: the contexts are [`Cx`] and [`CommitCx`].
pub trait ModuleCx<'id>: Resolve<'id> + sealed::Sealed {}

impl<'id, M: Module> ModuleCx<'id> for Cx<'id, M> {}

impl<'c, M: Module> ModuleCx<'c> for CommitCx<'c, M> {}

mod sealed {
    use core::ptr::NonNull;

    use crate::{bindings, dp::Module};

    /// What a module context holds for the links, out of the public surface.
    pub trait Sealed {
        /// The module's execution context, frozen while the context lives,
        /// with its absolute fields derived.
        fn module_ectx(&self) -> NonNull<bindings::module_ectx>;
    }

    impl<M: Module> Sealed for super::Cx<'_, M> {
        #[inline(always)]
        fn module_ectx(&self) -> NonNull<bindings::module_ectx> {
            self.ectx
        }
    }

    impl<M: Module> Sealed for super::CommitCx<'_, M> {
        #[inline(always)]
        fn module_ectx(&self) -> NonNull<bindings::module_ectx> {
            self.ectx
        }
    }

    /// What a prepare context holds for the slot and region handles, out of
    /// the public surface.
    pub trait Prepare {
        /// The worker of the context being committed, if known.
        fn prepare_worker(&self) -> Option<u64>;
    }

    impl<M: Module> Prepare for super::CommitCx<'_, M> {
        #[inline(always)]
        fn prepare_worker(&self) -> Option<u64> {
            self.worker()
        }
    }

    impl<O: crate::dp::Object> Prepare for super::ObjectCommitCx<'_, O> {
        #[inline(always)]
        fn prepare_worker(&self) -> Option<u64> {
            Some(self.worker())
        }
    }
}

/// Borrows initialized worker-local counter words as their sealed cell layout.
///
/// # Safety
///
/// The pointer retains the storage's provenance and is non-null and aligned,
/// even for an empty layout. Non-empty storage contains all initialized words
/// of the layout and stays live for the returned lifetime, within this worker's
/// call. Only this worker writes it. Overlapping accesses must use shared cells
/// or compatible raw accesses, never ordinary shared or exclusive references.
#[inline(always)]
unsafe fn counter_cells<'a, T: CounterLayout>(slot: *mut u64) -> &'a T::Storage {
    const {
        assert!(size_of::<T::Storage>() == T::VALUES * size_of::<u64>());
        assert!(align_of::<T::Storage>() == align_of::<u64>());
    }
    // SAFETY: TCB counter cells. Sealed layouts are transparent cells or
    // recursive arrays of them. The caller guarantees validity, locality,
    // compatible aliases and call-bounded liveness, including for empty views.
    unsafe { &*slot.cast::<T::Storage>() }
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

impl<T: CounterLayout> Counter<T> {
    /// Adds the value to the current worker's copy of the counter, one of a
    /// data-indexed set the handler picks by value; a counter named by a
    /// single field is added to through a handle in the prepared value.
    ///
    /// The same plain add as the handle's.
    #[inline(always)]
    pub fn add<'id, M: Module>(&'id self, cx: &Cx<'id, M>, value: T::Value) {
        // SAFETY: the context is frozen for the call and its counter storage
        // address was derived before release to the worker (attach: call,
        // frozen, publish); a built counter id indexes the module's counter
        // registry, whose storage of this context holds the counter's
        // aligned values next to each other, as many as the layout's, the
        // size it was registered with.
        let slot = unsafe { counter_value(cx.root, cx.ectx, self.id()) };
        // SAFETY: TCB direct cells. The resolved initialized values belong to
        // this worker for the call. Overlapping bound views also use cells.
        T::add(unsafe { counter_cells::<T>(slot) }, value);
    }

    /// The context's worker's values of the counter, for the prepared value.
    ///
    /// The handle holds the address C's `counter_get_address` returns.
    ///
    /// The handle is made with the prepare context, not the invocation context:
    ///
    /// ```compile_fail,E0308
    /// # use yanet_sys::dp::{Counter, Cx, Module};
    /// fn wrong<'id, M: Module<Config = Counter>>(cx: &Cx<'id, M>) {
    ///     let _ = cx.config().resolve(cx);
    /// }
    /// ```
    ///
    /// With the prepare context it is accepted:
    ///
    /// ```
    /// # use yanet_sys::dp::{CommitCx, Counter, Module};
    /// fn right<'p, M: Module<Config = Counter>>(cx: &CommitCx<'p, M>) {
    ///     let _ = cx.config().resolve(cx);
    /// }
    /// ```
    #[doc(alias = "counter_get_address")]
    #[inline(always)]
    pub fn resolve<'c, M: Module>(&'c self, cx: &CommitCx<'c, M>) -> CounterRef<'c, T> {
        // SAFETY: as for adding, for the pass instead of the call.
        let value = unsafe { counter_value(cx.root, cx.ectx, self.id()) };
        CounterRef {
            // SAFETY: the value lies in the counter storage block, not at
            // address zero.
            value: unsafe { NonNull::new_unchecked(value) },
            _brand: PhantomData,
            _layout: PhantomData,
        }
    }
}

/// Address of the context's worker's value of a counter of a tagged registry,
/// as C's `counter_get_address(id, module_ectx_counter_storage(ectx,
/// registry))`.
///
/// # Safety
///
/// As for [`counter_value`], for the runtime storages of the context instead
/// of its counter storage: the registry must be an index of the module's
/// tagged registries and the id an index of that registry.
#[inline(always)]
unsafe fn tagged_counter_value(
    root: ShmRoot,
    ectx: NonNull<bindings::module_ectx>,
    registry: u64,
    id: u64,
) -> *mut u64 {
    // SAFETY: as for the storage, and the id indexes its registry (caller
    // contract).
    unsafe { counter_get_address(root, tagged_counter_storage(root, ectx, registry), id) }
}

/// The context's worker's storage of a tagged registry, as C's
/// `module_ectx_counter_storage(ectx, registry)` finds it.
///
/// # Safety
///
/// The context must have its runtime storage addresses derived, one per tagged
/// registry of the module, and stay frozen while the result is used, and the
/// registry must be an index of the module's tagged registries.
#[inline(always)]
unsafe fn tagged_counter_storage(
    root: ShmRoot,
    ectx: NonNull<bindings::module_ectx>,
    registry: u64,
) -> *mut bindings::counter_storage {
    let ectx = ectx.as_ptr();
    // SAFETY: the context is frozen and its runtime storage addresses were
    // derived, one per tagged registry of the module (caller contract).
    unsafe {
        debug_assert!(
            registry < (*ectx).runtime_counter_storage_count,
            "a tagged registry beyond the registries of the module context"
        );
        let storages = root.ptr::<*mut bindings::counter_storage>((*ectx).abs_runtime_counter_storages_base.addr());
        root.ptr::<bindings::counter_storage>((*storages.add(registry as usize)).addr())
    }
}

impl<T> TaggedCounter<T>
where
    T: CounterLayout,
{
    /// Adds the value to the current worker's copy of the counter, one of a
    /// data-indexed set the handler picks by value, as a [`Counter`] adds.
    ///
    /// The same plain add as the handle's.
    #[doc(alias = "module_ectx_counter_storage")]
    #[inline(always)]
    pub fn add<'id, M>(&'id self, cx: &Cx<'id, M>, value: T::Value)
    where
        M: Module,
    {
        // SAFETY: the context is frozen for the call and its runtime storage
        // addresses were derived before release to the worker (attach: call,
        // frozen, publish), one per tagged registry of the module (attach:
        // counters); a built tagged counter names a tagged registry of the
        // module and an id of it, whose storage of this context holds the
        // counter's aligned values next to each other, as many as the
        // layout's, the size it was registered with.
        let slot = unsafe { tagged_counter_value(cx.root, cx.ectx, self.registry(), self.id()) };
        // SAFETY: TCB tagged direct cells. The resolved initialized values
        // belong to this worker for the call, with only compatible cell
        // aliases.
        T::add(unsafe { counter_cells::<T>(slot) }, value);
    }

    /// The context's worker's values of the counter, for the prepared value,
    /// as a [`Counter`] resolves.
    ///
    /// The handle holds the address C's `counter_get_address` returns in the
    /// storage `module_ectx_counter_storage` finds.
    #[doc(alias = "module_ectx_counter_storage")]
    #[inline(always)]
    pub fn resolve<'c, M>(&'c self, cx: &CommitCx<'c, M>) -> CounterRef<'c, T>
    where
        M: Module,
    {
        // SAFETY: as for adding, for the pass instead of the call.
        let value = unsafe { tagged_counter_value(cx.root, cx.ectx, self.registry(), self.id()) };
        CounterRef {
            // SAFETY: the value lies in the counter storage block, not at
            // address zero.
            value: unsafe { NonNull::new_unchecked(value) },
            _brand: PhantomData,
            _layout: PhantomData,
        }
    }
}

impl<T> TaggedCounters<T>
where
    T: CounterLayout,
{
    /// The context's worker's values of the counters, for the prepared value:
    /// the handle table of the registry's storage, which the handler adds
    /// through by id, a single hop per packet.
    ///
    /// The table is the one in the storage C's `module_ectx_counter_storage`
    /// finds, which a C module hoists into its prepared state, as acl's
    /// `rules_handles`. A registry the context holds no storage for, which that
    /// lookup refuses too, gives an empty handle, through which every add
    /// counts nowhere.
    #[doc(alias = "module_ectx_counter_storage")]
    #[inline(always)]
    pub fn resolve<'c, M>(&'c self, cx: &CommitCx<'c, M>) -> TaggedCountersRef<'c, T>
    where
        M: Module,
    {
        // SAFETY: the context is live and frozen for the pass (attach: call,
        // frozen).
        let registries = unsafe { (*cx.ectx.as_ptr()).runtime_counter_storage_count };
        let (table, len) = if self.is_empty() || self.registry() >= registries {
            // No counters, or no storage of the context for their registry: no
            // id is below the bound, so the table is never read.
            (NonNull::dangling(), 0)
        } else {
            // SAFETY: the context is frozen for the pass and its runtime
            // storage addresses were derived before (attach: call, frozen,
            // publish), one per tagged registry of the module, the field's
            // among them (attach: counters); a built field with counters names
            // the registry they were registered in, whose storage of this
            // context holds a handle per counter of the registry, those of the
            // configuration first. The table is a live block, not at address
            // zero.
            let table = unsafe {
                let storage = tagged_counter_storage(cx.root, cx.ectx, self.registry());
                NonNull::new_unchecked(counter_value_handles(cx.root, storage))
            };
            (table, self.len())
        };
        TaggedCountersRef {
            table,
            len,
            _brand: PhantomData,
            _layout: PhantomData,
        }
    }
}

/// Address of the context's worker's value of a counter of the object, as
/// C's `counter_get_address(id, ADDR_OF(&ectx->counter_storage))`.
///
/// The context keeps its counter storage as a relative address only: C
/// derives no absolute twin of it.
///
/// # Safety
///
/// The context must be the object's context being committed, frozen while
/// the result is used, with the counter storage C spawned from the object's
/// registry when it built the context, and the id must be an index of that
/// registry.
#[inline(always)]
unsafe fn object_counter_value(root: ShmRoot, ectx: NonNull<bindings::object_ectx>, id: u64) -> *mut u64 {
    // SAFETY: the context is frozen and its storage slot holds the offset to
    // the storage C spawned for it (caller contract). The offset is read as
    // an integer, never as a pointer.
    unsafe {
        let slot = &raw const (*ectx.as_ptr()).counter_storage;
        let offset = slot.cast::<isize>().read();
        debug_assert!(offset != 0, "an object context without a counter storage");
        counter_get_address(root, root.rel(slot.addr(), offset), id)
    }
}

impl<T> ObjectCounter<T>
where
    T: CounterLayout,
{
    /// The context's worker's values of the object's counter, for the
    /// object's prepared value.
    ///
    /// The handle holds the address C's `counter_get_address` returns in the
    /// counter storage of the object's context.
    ///
    /// Only the object's prepare context resolves it, not a module's:
    ///
    /// ```compile_fail,E0308
    /// # use yanet_sys::dp::{CommitCx, Module, ObjectCounter};
    /// fn wrong<'p, M: Module<Config = ObjectCounter>>(cx: &CommitCx<'p, M>) {
    ///     let _ = cx.config().resolve(cx);
    /// }
    /// ```
    ///
    /// With the object's prepare context it is accepted:
    ///
    /// ```
    /// # use yanet_sys::dp::{Object, ObjectCommitCx, ObjectCounter};
    /// fn right<'p, O: Object<Config = ObjectCounter>>(cx: &ObjectCommitCx<'p, O>) {
    ///     let _ = cx.config().resolve(cx);
    /// }
    /// ```
    #[doc(alias = "counter_get_address")]
    #[inline(always)]
    pub fn resolve<'c, O>(&'c self, cx: &ObjectCommitCx<'c, O>) -> CounterRef<'c, T>
    where
        O: Object,
    {
        // SAFETY: the context is the object's one being committed, frozen for
        // the pass (constructor contract, attach: frozen), and C spawned its
        // counter storage from the object's registry when it built the
        // context (attach: counters); a built object counter id indexes that
        // registry, whose storage holds the counter's aligned values next to
        // each other, as many as the layout's, the size it was registered
        // with.
        let value = unsafe { object_counter_value(cx.root, cx.ectx, self.id()) };
        CounterRef {
            // SAFETY: the value lies in the counter storage block, not at
            // address zero.
            value: unsafe { NonNull::new_unchecked(value) },
            _brand: PhantomData,
            _layout: PhantomData,
        }
    }
}

impl Device {
    /// Routing target that transmits through the device, `None` when the
    /// device is not part of the running generation.
    ///
    /// The target is the one C's `module_ectx_device_target` finds.
    #[doc(alias = "module_ectx_device_target")]
    #[inline(always)]
    pub fn resolve<'id, M: Module>(&'id self, cx: &Cx<'id, M>) -> Option<DeviceRef<'id>> {
        device_target(cx, self, false)
    }

    /// Routing target that re-enters the input of the device, `None` when
    /// the device is not part of the running generation.
    ///
    /// The target is the one C's `module_ectx_device_target` finds.
    #[doc(alias = "module_ectx_device_target")]
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

impl<O: Object> ObjectLink<O> {
    /// The linked object's configuration in the running generation, as C's
    /// `object_link_get_address(...)->abs_object_ectx->abs_cp_object` finds
    /// it.
    ///
    /// Always there: the link was recorded by the build of this configuration,
    /// and C installs no configuration whose link the generation does not
    /// resolve to an object of the type and name.
    #[doc(alias = "object_link_get_address")]
    #[inline(always)]
    pub fn get<'id>(&'id self, cx: &impl ModuleCx<'id>) -> &'id O::Config {
        let root = cx.shm_root();
        let object_ectx = linked_object_ectx(cx, self.index());
        // SAFETY: the object context is frozen and its absolute fields were
        // derived before the module's (attach: call, frozen); the object is
        // one of type `O`, as C resolves the link by its type name, whose
        // body is frozen except for its atomics and mutex payloads and outlives
        // the generation (attach: binding, frozen, reclaim).
        unsafe {
            let cp_object = (*object_ectx).abs_cp_object;
            (*root.ptr::<ObjectConfig<O::Config>>(cp_object.addr())).body()
        }
    }

    /// The linked object's prepared value for the context's worker in the
    /// running generation, as the object's prepare step made it.
    ///
    /// The step made it under its own commit brand; it is read here under the
    /// module context's. What it may borrow is the object's memory, which the
    /// generation the module context runs in holds, so it lives at least as
    /// long as that context.
    #[inline(always)]
    pub fn prepared<'id>(&'id self, cx: &impl ModuleCx<'id>) -> &'id O::Prepared<'id> {
        if size_of::<O::Prepared<'id>>() == 0 {
            // C allocates no buffer for an object without a prepared size.
            // SAFETY: a zero-sized value is read from any aligned address.
            return unsafe { NonNull::dangling().as_ref() };
        }
        let root = cx.shm_root();
        let object_ectx = linked_object_ectx(cx, self.index());
        // SAFETY: as for the configuration; the object declares a prepared
        // size, so C allocated the buffer of every context of the object, and
        // its commit pass wrote the value before the module contexts linking
        // it were derived (attach: call, align); the buffer is frozen once the
        // pass is done. The value borrows at most the object's memory, which
        // outlives the generation of the call (attach: reclaim), so the
        // module's brand does not outlive what it borrows.
        unsafe {
            let prepared = (*object_ectx).abs_object_prepared;
            debug_assert!(!prepared.is_null(), "no prepared buffer of an object that declares one");
            &*root.ptr::<O::Prepared<'id>>(prepared.addr())
        }
    }
}

/// The execution context of the object behind a link of the module, for the
/// context's worker.
#[inline(always)]
fn linked_object_ectx<'id>(cx: &impl ModuleCx<'id>, index: u64) -> *const bindings::object_ectx {
    let root = cx.shm_root();
    let ectx = cx.module_ectx().as_ptr();
    // SAFETY: the module context is frozen with its absolute fields derived
    // (attach: frozen, publish); a built link index is below the link count,
    // as the configuration this context runs recorded the link and C
    // installed it resolved.
    unsafe {
        debug_assert!(
            index < (*ectx).object_link_count,
            "an object link beyond the links of the module context"
        );
        let links = root.ptr::<bindings::module_object_link_ectx>((*ectx).abs_object_links.addr());
        root.ptr::<bindings::object_ectx>((*links.add(index as usize)).abs_object_ectx.addr())
    }
}

#[cfg(test)]
mod tests {
    extern crate std;

    use core::{
        assert_matches,
        cell::Cell,
        marker::PhantomData,
        panic::{RefUnwindSafe, UnwindSafe},
        ptr::NonNull,
    };

    use super::{BoundCounter, BoundTaggedCounters, CounterRef, TaggedCountersRef, counter_cells};
    use crate::dp::{CounterLayout, Gauge, Traffic};

    /// A worker-local view over owner-local test cells.
    fn bound<T: CounterLayout>(cells: &T::Storage) -> BoundCounter<'_, T> {
        BoundCounter {
            cells,
            _brand: PhantomData,
            _local: PhantomData,
            _layout: PhantomData,
        }
    }

    /// Pins the positive traits and pointer niches independently of cell Copy.
    fn assert_traits<T: CounterLayout>() {
        fn copy_unwind<T: Copy + Clone + UnwindSafe + RefUnwindSafe>() {}
        fn moves<T: Send>() {}
        copy_unwind::<BoundCounter<'_, T>>();
        copy_unwind::<BoundTaggedCounters<'_, T>>();
        copy_unwind::<CounterRef<'_, T>>();
        copy_unwind::<TaggedCountersRef<'_, T>>();
        moves::<CounterRef<'_, T>>();
        moves::<TaggedCountersRef<'_, T>>();
        assert_eq!(size_of::<usize>(), size_of::<BoundCounter<'_, T>>());
        assert_eq!(size_of::<usize>(), size_of::<Option<BoundCounter<'_, T>>>());
        assert_eq!(align_of::<usize>(), align_of::<BoundCounter<'_, T>>());
        assert_eq!(size_of::<usize>(), size_of::<CounterRef<'_, T>>());
        assert_eq!(size_of::<usize>(), size_of::<Option<CounterRef<'_, T>>>());
    }

    #[test]
    fn test_bound_counter_traits_and_niches_for_all_layouts() {
        struct NotLayout;
        fn moves_copy<T: Copy + Send>() {}
        moves_copy::<CounterRef<'_, NotLayout>>();
        moves_copy::<TaggedCountersRef<'_, NotLayout>>();
        assert_traits::<u64>();
        assert_traits::<Traffic>();
        assert_traits::<Gauge>();
        assert_traits::<[[Traffic; 3]; 2]>();
        assert_traits::<[u64; 0]>();
        assert_traits::<[Traffic; 0]>();
        assert_traits::<[[Gauge; 0]; 3]>();
        assert_traits::<[[Traffic; 3]; 0]>();
    }

    #[test]
    fn test_bound_counter_copied_gauge_wrap_and_unwind_captures() {
        let cells = Cell::new(0);
        let gauge = bound::<Gauge>(&cells);
        let copy = gauge;
        copy.sub(1);
        assert_eq!(u64::MAX, gauge.get());
        assert_matches!(std::panic::catch_unwind(|| gauge.add(2)), Ok(()));
        assert_matches!(std::panic::catch_unwind(move || copy.sub(3)), Ok(()));
        assert_eq!(u64::MAX - 1, gauge.get());
        assert_matches!(
            std::panic::catch_unwind(move || {
                copy.add(5);
                panic!("after the completed counter update");
            }),
            Err(_),
        );
        assert_eq!(3, gauge.get());
        gauge.sub(4);
        assert_eq!(u64::MAX, gauge.get());
    }

    #[test]
    fn test_bound_counter_nested_copied_views_overlap() {
        let cells = core::array::from_fn(|_| core::array::from_fn(|_| [Cell::new(0), Cell::new(0)]));
        let pairs = bound::<[[Traffic; 2]; 3]>(&cells);
        let copy = pairs;
        pairs.at::<2>().at::<1>().add(Traffic::packet(7));
        copy.at::<2>()
            .at::<1>()
            .add(Traffic { packets: u64::MAX, bytes: u64::MAX });
        assert_eq!(Traffic { packets: 0, bytes: 6 }, pairs.get()[2][1]);
        assert_eq!(Traffic::default(), pairs.at::<0>().at::<0>().get());
    }

    #[test]
    fn test_counter_cells_nested_views_match_flat_word_offsets() {
        let flat = Cell::new([11u64, 12, 21, 22, 31, 32, 41, 42]);
        let slot = flat.as_ptr().cast::<u64>();
        // SAFETY: the flat buffer holds eight initialized, aligned words,
        // local and live for the nested view, with only compatible cell access.
        let cells = unsafe { counter_cells::<[[Traffic; 2]; 2]>(slot) };
        let pairs = bound::<[[Traffic; 2]; 2]>(cells);
        assert_eq!(
            [
                [Traffic { packets: 11, bytes: 12 }, Traffic { packets: 21, bytes: 22 }],
                [Traffic { packets: 31, bytes: 32 }, Traffic { packets: 41, bytes: 42 }],
            ],
            pairs.get(),
        );
        pairs.at::<1>().at::<0>().add(Traffic { packets: 5, bytes: 7 });
        assert_eq!([11, 12, 21, 22, 36, 39, 41, 42], flat.get());
        pairs.at::<0>().at::<1>().add(Traffic { packets: 3, bytes: 9 });
        assert_eq!([11, 12, 24, 31, 36, 39, 41, 42], flat.get());
        assert_eq!(Traffic { packets: 36, bytes: 39 }, pairs.get()[1][0]);
    }

    #[test]
    fn test_counter_cells_raw_and_bound_aliases_overlap() {
        let cell = Cell::new(0);
        let slot = cell.as_ptr();
        // SAFETY: the initialized cell is aligned and local for both views.
        let first = unsafe { counter_cells::<Gauge>(slot) };
        let view = bound::<Gauge>(first);
        // SAFETY: the same live storage permits shared cells and raw word
        // accesses, without ordinary shared or exclusive references.
        let second = unsafe { counter_cells::<Gauge>(slot) };
        view.sub(1);
        // SAFETY: the original pointer reads the live initialized word locally.
        assert_eq!(u64::MAX, unsafe { slot.read() });
        // SAFETY: a raw write to the cell payload is compatible with the views.
        unsafe { slot.write(7) };
        assert_eq!(7, view.get());
        Gauge::add(second, 2);
        // SAFETY: the original pointer observes the completed cell update.
        assert_eq!(9, unsafe { slot.read() });
        // SAFETY: the live cell payload permits another raw write.
        unsafe { slot.write(u64::MAX) };
        view.add(2);
        assert_eq!(1, Gauge::read(second));
        assert_eq!(1, view.get());
    }

    #[test]
    fn test_counter_cells_empty_dangling_views_access_no_storage() {
        let slot = NonNull::<u64>::dangling().as_ptr();
        // SAFETY: a zero-sized layout needs no storage, only word alignment.
        let scalar = unsafe { counter_cells::<[u64; 0]>(slot) };
        bound::<[u64; 0]>(scalar).add([]);
        // SAFETY: the same aligned dangling pointer suffices for empty traffic.
        let traffic = unsafe { counter_cells::<[Traffic; 0]>(slot) };
        assert_eq!(
            [Traffic { packets: 0, bytes: 0 }; 0],
            bound::<[Traffic; 0]>(traffic).get()
        );
        // SAFETY: every row is empty, so there are no words to access.
        let nested = unsafe { counter_cells::<[[Gauge; 0]; 3]>(slot) };
        let nested = bound::<[[Gauge; 0]; 3]>(nested);
        nested.at::<2>().add([]);
        assert_eq!([[0u64; 0]; 3], nested.get());
        // SAFETY: an empty outer array has no storage either.
        let outer = unsafe { counter_cells::<[[Traffic; 3]; 0]>(slot) };
        assert_eq!(
            [[Traffic { packets: 0, bytes: 0 }; 3]; 0],
            bound::<[[Traffic; 3]; 0]>(outer).get()
        );
    }
}
