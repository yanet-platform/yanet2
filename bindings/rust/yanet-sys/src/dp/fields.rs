//! Configuration field types backed by C registries of the module, and the
//! per-worker state of a configuration.

use core::{marker::PhantomData, ptr::NonNull};

use yanet_shm::{
    rel::RelPtr,
    root::ShmRoot,
    shm::{MAX_ALIGN, ShmLayout},
};

use crate::dp::{
    cx::{Cx, PrepareCx},
    module::Module,
    object::Object,
};

/// Per-worker counter of the module, registered by the control plane as a C
/// counter of as many values as its layout `T` holds.
///
/// Holds the counter's index in the module's C counter registry; the
/// dataplane adds a `T` to the current worker's values without atomic
/// read-modify-write, as C modules do. `T` is a single `u64` by default, or
/// [`Traffic`] for the packets and bytes every module counts its traffic with.
///
/// An add takes a value of the layout, so a counter of traffic is not added a
/// bare number:
///
/// ```compile_fail,E0308
/// # use yanet_sys::dp::{Counter, Cx, Module, Traffic};
/// fn count<'id, M: Module<Config = Counter<Traffic>>>(cx: &Cx<'id, M>) {
///     cx.config().add(cx, 1);
/// }
/// ```
///
/// It takes the packets and bytes:
///
/// ```
/// # use yanet_sys::dp::{Counter, Cx, Module, Traffic};
/// fn count<'id, M: Module<Config = Counter<Traffic>>>(cx: &Cx<'id, M>, len: u64) {
///     cx.config().add(cx, Traffic::packet(len));
/// }
/// ```
#[repr(C)]
pub struct Counter<T = u64> {
    id: u64,
    // What one worker's values are; the configuration stores the index only.
    _layout: PhantomData<fn() -> T>,
}

impl<T> Counter<T> {
    /// Index in the module's counter registry.
    #[inline(always)]
    pub fn id(&self) -> u64 {
        self.id
    }
}

/// What one worker's values of a [`Counter`] are, and how a value of the
/// layout adds to them.
///
/// Sealed: the layouts are a `u64` and [`Traffic`]; a module adds none of its
/// own:
///
/// ```compile_fail,E0277
/// # use yanet_sys::dp::CounterLayout;
/// #[derive(Clone, Copy)]
/// struct Mine(u64);
/// impl CounterLayout for Mine {
///     const VALUES: usize = 1;
///     unsafe fn add_to(self, _: *mut u64) {}
/// }
/// ```
pub trait CounterLayout: Copy + 'static + sealed::Sealed {
    /// Number of values, the size C registers the counter with.
    const VALUES: usize;

    /// Adds each value to the worker's value at the same place.
    ///
    /// # Safety
    ///
    /// The address is the first of this worker's values of the counter, as
    /// many as the layout holds, aligned and next to each other, which only
    /// this worker writes and which stay live for the call.
    #[doc(hidden)]
    unsafe fn add_to(self, slot: *mut u64);
}

/// The packets and bytes of the traffic a counter counts, as the counters
/// C keeps of every module, device and pipeline: the packets first.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
#[repr(C)]
pub struct Traffic {
    pub packets: u64,
    pub bytes: u64,
}

impl Traffic {
    /// One packet of the given length in bytes.
    #[inline(always)]
    pub fn packet(bytes: u64) -> Self {
        Self { packets: 1, bytes }
    }
}

impl CounterLayout for u64 {
    const VALUES: usize = 1;

    #[inline(always)]
    unsafe fn add_to(self, slot: *mut u64) {
        // SAFETY: the counter's only value (caller contract).
        unsafe { add_value(slot, self) };
    }
}

impl CounterLayout for Traffic {
    const VALUES: usize = 2;

    #[inline(always)]
    unsafe fn add_to(self, slot: *mut u64) {
        let Self { packets, bytes } = self;
        // SAFETY: the counter's two values (caller contract), in the order
        // of the fields.
        unsafe {
            add_value(slot, packets);
            add_value(slot.add(1), bytes);
        }
    }
}

/// Adds to one value of the worker's counter in place.
///
/// # Safety
///
/// As for [`CounterLayout::add_to`], for one value.
#[inline(always)]
unsafe fn add_value(slot: *mut u64, value: u64) {
    // SAFETY: the address is an aligned value of this worker's counter
    // storage, written only by this worker and live as long as the generation
    // the address was resolved for (caller contract). The control plane reads
    // it with memcpy (lib/controlplane/agent/counters.c), as it does for the
    // plain `+=` of a C module.
    unsafe { *slot = (*slot).wrapping_add(value) };
}

// The traffic layout is the C values it adds to: `u64`s with nothing between.
const _: () = {
    assert!(size_of::<Traffic>() == <Traffic as CounterLayout>::VALUES * size_of::<u64>());
    assert!(align_of::<Traffic>() == align_of::<u64>());
};

mod sealed {
    pub trait Sealed {}

    impl Sealed for u64 {}

    impl Sealed for super::Traffic {}
}

/// A device linked to the module configuration, by its index in the
/// module's device list.
///
/// The dataplane resolves it to the device's routing target of the running
/// generation, which is absent when the device is not part of it.
#[repr(C)]
pub struct Device {
    index: u64,
}

impl Device {
    /// Index in the module's device list.
    #[inline(always)]
    pub fn index(&self) -> u64 {
        self.index
    }
}

/// An object of type `O` linked to the module configuration by name, by its
/// index in the module's object links.
///
/// The dataplane resolves it to the object of that type and name the running
/// generation holds, which is always there: C installs no configuration whose
/// link it cannot resolve.
#[repr(C)]
pub struct ObjectLink<O> {
    index: u64,
    // What the link resolves to; the configuration stores the index only.
    _object: PhantomData<fn() -> O>,
}

impl<O> ObjectLink<O> {
    /// Index in the module's object links.
    #[inline(always)]
    pub fn index(&self) -> u64 {
        self.index
    }
}

// SAFETY: a registry index accepts every bit pattern, and the layout is a
// zero-sized marker; the control plane writes the id the module's counter
// registry assigned.
unsafe impl<T: CounterLayout> ShmLayout for Counter<T> {}

// SAFETY: a device index accepts every bit pattern; the control plane writes
// the index the module's device list assigned.
unsafe impl ShmLayout for Device {}

// SAFETY: a link index accepts every bit pattern, and the object type is a
// zero-sized marker; the control plane writes the index the module's object
// links assigned.
unsafe impl<O: Object> ShmLayout for ObjectLink<O> {}

/// Bytes of the widest cache line a slot of [`PerWorker`] keeps to itself.
const SLOT_LINE: usize = 128;

/// One value of `T` per dataplane worker; the control plane builds every slot
/// from one initial value, a prepare step makes the handle of its worker's slot
/// with [`Self::handle`], and the handler reads and writes the slot through it.
///
/// The handler borrows its slot in place, shared to read and exclusive to
/// write, while its call context is borrowed the same way, so no reference to
/// a slot outlives the handler call. A prepare step makes each handle for its
/// own worker's slot, so a worker never aliases another's slot. A slot holds
/// only plain values: a value that owns a block, names a registry entry or
/// embeds a C object is not `Copy`, so it cannot be a slot.
///
/// The first slot starts on a 128-byte line and the slots are
/// [`Self::STRIDE`] apart, so no slot shares a cache line of up to 128 bytes
/// with another worker's slot or with memory outside the slots.
///
/// Slots live as long as the configuration that holds them: a module
/// configuration rebuilt on an update starts its slots again from the initial
/// value. State that must survive updates belongs in an object.
#[repr(C)]
pub struct PerWorker<T> {
    slots: RelPtr<T>,
    workers: u64,
}

impl<T: Copy + ShmLayout> PerWorker<T> {
    /// Bytes from one worker's slot to the next: the value rounded up to
    /// whole 128-byte lines.
    pub const STRIDE: usize = size_of::<T>().next_multiple_of(SLOT_LINE);

    /// Bytes of the slot block of the given number of workers, `None` when
    /// the size overflows.
    ///
    /// The block starts on a 64-byte line only (a sanitizer build shifts it
    /// by its red zone), so it holds the slots and the most the first slot
    /// may lie past the block's start.
    #[doc(hidden)]
    pub const fn block_size(workers: usize) -> Option<usize> {
        match workers.checked_mul(Self::STRIDE) {
            Some(slots) => slots.checked_add(SLOT_LINE - MAX_ALIGN),
            None => None,
        }
    }

    /// Address of the first slot of the slot block at the given address:
    /// the next 128-byte line.
    #[doc(hidden)]
    #[inline(always)]
    pub const fn first_slot(block: usize) -> usize {
        block.wrapping_add(SLOT_LINE - 1) & !(SLOT_LINE - 1)
    }

    /// Number of slots, one per worker.
    #[doc(hidden)]
    #[inline(always)]
    pub fn worker_count(&self) -> u64 {
        self.workers
    }

    /// Stored offset of the slot block, zero when there is none.
    #[doc(hidden)]
    #[inline(always)]
    pub fn offset(&self) -> isize {
        self.slots.offset()
    }

    /// A handle on the slot of the prepare context's worker, for its prepared
    /// value: the only way to a slot, which the handler reads and writes
    /// through it; `None` when a module's context is not among its
    /// generation's worker contexts.
    ///
    /// ```
    /// use yanet_sys::dp::{Batch, CommitCx, Cx, Module, PerWorker, Verdict, WorkerSlot};
    /// struct Mine;
    /// impl Module for Mine {
    ///     const NAME: &'static str = "mine";
    ///     type Config = PerWorker<u64>;
    ///     type Prepared<'p> = Option<WorkerSlot<'p, u64>>;
    ///     fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
    ///         if let Some(seen) = cx.prepared() {
    ///             *seen.get_mut(cx) += 1;
    ///         }
    ///         batch.dispatch(|_| Verdict::Pass);
    ///     }
    ///     fn prepare<'p>(cx: &CommitCx<'p, Self>) -> Option<WorkerSlot<'p, u64>> {
    ///         cx.config().handle(cx)
    ///     }
    /// }
    /// ```
    ///
    /// Only a prepare context makes one:
    ///
    /// ```compile_fail,E0277
    /// # use yanet_sys::dp::{Cx, Module, PerWorker};
    /// fn late<'id, M: Module<Config = PerWorker<u64>>>(cx: &Cx<'id, M>) {
    ///     let _ = cx.config().handle(cx);
    /// }
    /// ```
    #[inline(always)]
    pub fn handle<'p>(&'p self, cx: &impl PrepareCx<'p>) -> Option<WorkerSlot<'p, T>> {
        let worker = cx.prepare_worker()?;
        let slot = self.slot_at(cx.shm_root(), worker);
        Some(WorkerSlot {
            // SAFETY: a slot lies in the slot block, not at address zero.
            slot: unsafe { NonNull::new_unchecked(slot) },
            _brand: PhantomData,
        })
    }

    /// The slot of the worker of the given index.
    #[inline(always)]
    fn slot_at(&self, root: ShmRoot, worker: u64) -> *mut T {
        debug_assert!(worker < self.workers, "a worker past the slots");
        let field = core::ptr::from_ref(self).addr();
        let first = Self::first_slot(field.wrapping_add_signed(self.slots.offset()));
        let slot = root.ptr::<T>(first.wrapping_add((worker as usize).wrapping_mul(Self::STRIDE)));
        debug_assert!(slot.is_aligned(), "misaligned per-worker slot");
        slot
    }
}

/// The slot of one worker of a [`PerWorker`], made by that worker's prepare
/// step and kept in its prepared value; the handler borrows the slot through
/// it with its call context, shared to read and exclusive to write.
///
/// The call context is a branded token of the call and the handle a cell it
/// owns, as in GhostCell or qcell: every access to a slot needs the context,
/// so a shared borrow of the context lends the slot shared and a mutable one
/// lends it exclusive. A small value is copied out of a shared borrow.
///
/// Only the handler's call context of the same brand reaches the slot: the
/// one whose prepared value, or linked object's prepared value, the handle
/// was read from. A prepare step makes and stores the handle but cannot use
/// it, since it runs while the worker may still run the previous generation,
/// which shares the slot:
///
/// ```compile_fail,E0308
/// # use yanet_sys::dp::{CommitCx, Module, PerWorker};
/// fn early<'p, M: Module<Config = PerWorker<u64>>>(cx: &CommitCx<'p, M>) -> u64 {
///     cx.config().handle(cx).map_or(0, |seen| *seen.get(cx))
/// }
/// ```
///
/// The handler uses the handle of its prepared value:
///
/// ```
/// # use yanet_sys::dp::{Cx, Module, WorkerSlot};
/// fn count<'id, M>(cx: &Cx<'id, M>) -> u64
/// where
///     M: Module<Prepared<'id> = Option<WorkerSlot<'id, u64>>>,
/// {
///     cx.prepared().map_or(0, |seen| *seen.get(cx))
/// }
/// ```
///
/// Copies and moves are harmless for the same reason, so the handle is `Copy`
/// and `Send`, as the prepared value travels from the assigner thread to its
/// worker. It has no `Debug`: it holds only an address, and its value is read
/// only in the call.
///
/// It has no shared-memory layout, so it is never a slot or a configuration
/// field, which would outlive the block it points into:
///
/// ```compile_fail,E0277
/// # use yanet_shm::shm::ShmLayout;
/// # use yanet_sys::dp::WorkerSlot;
/// fn layout<T: ShmLayout>() {}
/// layout::<WorkerSlot<'static, u64>>();
/// ```
///
/// The field it comes from has one:
///
/// ```
/// # use yanet_shm::shm::ShmLayout;
/// # use yanet_sys::dp::PerWorker;
/// fn layout<T: ShmLayout>() {}
/// layout::<PerWorker<u64>>();
/// ```
pub struct WorkerSlot<'p, T: Copy + ShmLayout> {
    slot: NonNull<T>,
    // Invariant brand of the context that made or read the handle: only the
    // call context of the same brand, on the handle's worker, uses it.
    _brand: PhantomData<fn(&'p ()) -> &'p ()>,
}

impl<T: Copy + ShmLayout> Clone for WorkerSlot<'_, T> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<T: Copy + ShmLayout> Copy for WorkerSlot<'_, T> {}

// SAFETY: the handle is an address and touches nothing by itself; every read
// and write takes the call context of the handle's brand, which is neither
// `Send` nor `Sync` and runs on the handle's worker.
unsafe impl<T: Copy + ShmLayout> Send for WorkerSlot<'_, T> {}

impl<'id, T: Copy + ShmLayout> WorkerSlot<'id, T> {
    /// The slot itself, borrowed shared while the call context stays
    /// borrowed.
    ///
    /// Shared borrows of the slot live together:
    ///
    /// ```
    /// # use yanet_sys::dp::{Cx, Module, WorkerSlot};
    /// fn sum<'id, M>(cx: &Cx<'id, M>) -> u64
    /// where
    ///     M: Module<Prepared<'id> = Option<WorkerSlot<'id, [u64; 96]>>>,
    /// {
    ///     cx.prepared().map_or(0, |slot| {
    ///         let first = slot.get(cx);
    ///         let second = slot.get(cx);
    ///         first[0] + second[1]
    ///     })
    /// }
    /// ```
    ///
    /// No exclusive borrow is taken while one lives:
    ///
    /// ```compile_fail,E0502
    /// # use yanet_sys::dp::{Cx, Module, WorkerSlot};
    /// fn poke<'id, M>(cx: &mut Cx<'id, M>)
    /// where
    ///     M: Module<Prepared<'id> = Option<WorkerSlot<'id, [u64; 96]>>>,
    /// {
    ///     if let Some(slot) = cx.prepared() {
    ///         let values = slot.get(cx);
    ///         let other = slot.get_mut(cx);
    ///         other[0] = values[1];
    ///     }
    /// }
    /// ```
    ///
    /// A value copied out does not hold the borrow:
    ///
    /// ```
    /// # use yanet_sys::dp::{Counter, Cx, Module, WorkerSlot};
    /// fn count<'id, M>(cx: &mut Cx<'id, M>)
    /// where
    ///     M: Module<Config = Counter, Prepared<'id> = Option<WorkerSlot<'id, u64>>>,
    /// {
    ///     if let Some(slot) = cx.prepared() {
    ///         let seen = *slot.get(cx);
    ///         cx.config().add(cx, seen);
    ///         *slot.get_mut(cx) = 0;
    ///     }
    /// }
    /// ```
    ///
    /// The borrow cannot outlive the borrow of the context:
    ///
    /// ```compile_fail,E0621
    /// # use yanet_sys::dp::{Cx, Module, WorkerSlot};
    /// fn keep<'id, M: Module>(cx: &Cx<'id, M>, slot: WorkerSlot<'id, u64>) -> &'id u64 {
    ///     slot.get(cx)
    /// }
    /// ```
    #[inline(always)]
    pub fn get<'a, M: Module>(&self, _cx: &'a Cx<'id, M>) -> &'a T {
        // SAFETY: a prepare step made the handle for its context's worker, and
        // it reached this call only through the prepared value of that context
        // or of the object context it links, under the call's brand, so the
        // slot is the call's worker's: aligned, written whole by the build, in
        // a block the configuration or object holds at least as long as the
        // call's generation (attach: reclaim) and touched by no other thread
        // (attach: frozen). Nothing writes the slot while the borrow lives:
        // every write takes the context mutably, which stays shared-borrowed
        // for as long. The context is the call's own, so the borrow ends
        // within the call.
        unsafe { self.slot.as_ref() }
    }

    /// The slot itself, borrowed while the call context stays mutably borrowed.
    ///
    /// Every access to a slot needs the context, so while the context is
    /// mutably borrowed the returned reference is the only way to the slot.
    ///
    /// While the borrow lives, the context reaches no slot, this one included:
    ///
    /// ```compile_fail,E0502
    /// # use yanet_sys::dp::{Cx, Module, WorkerSlot};
    /// fn peek<'id, M>(cx: &mut Cx<'id, M>)
    /// where
    ///     M: Module<Prepared<'id> = Option<WorkerSlot<'id, [u64; 96]>>>,
    /// {
    ///     if let Some(slot) = cx.prepared() {
    ///         let values = slot.get_mut(cx);
    ///         let other = slot.get(cx);
    ///         values[0] = other[1];
    ///     }
    /// }
    /// ```
    ///
    /// Nor does it borrow the slot again, through a copy of the handle either:
    ///
    /// ```compile_fail,E0499
    /// # use yanet_sys::dp::{Cx, Module, WorkerSlot};
    /// fn twice<'id, M>(cx: &mut Cx<'id, M>)
    /// where
    ///     M: Module<Prepared<'id> = Option<WorkerSlot<'id, [u64; 96]>>>,
    /// {
    ///     if let Some(slot) = cx.prepared() {
    ///         let copy = *slot;
    ///         let first = slot.get_mut(cx);
    ///         let second = copy.get_mut(cx);
    ///         first[0] = second[1];
    ///     }
    /// }
    /// ```
    ///
    /// The borrow does not outlive the borrow of the context, so it never
    /// leaves the call:
    ///
    /// ```compile_fail,E0621
    /// # use yanet_sys::dp::{Cx, Module, WorkerSlot};
    /// fn keep<'id, M: Module>(cx: &mut Cx<'id, M>, slot: WorkerSlot<'id, [u64; 96]>) -> &'id mut [u64; 96] {
    ///     slot.get_mut(cx)
    /// }
    /// ```
    ///
    /// Once the borrow is last used, the context is free again:
    ///
    /// ```
    /// # use yanet_sys::dp::{Counter, Cx, Module, WorkerSlot};
    /// fn count<'id, M>(cx: &mut Cx<'id, M>)
    /// where
    ///     M: Module<Config = Counter, Prepared<'id> = Option<WorkerSlot<'id, [u64; 96]>>>,
    /// {
    ///     if let Some(slot) = cx.prepared() {
    ///         let values = slot.get_mut(cx);
    ///         values[0] += 1;
    ///         cx.config().add(cx, 1);
    ///     }
    /// }
    /// ```
    #[inline(always)]
    pub fn get_mut<'a, M: Module>(&self, _cx: &'a mut Cx<'id, M>) -> &'a mut T {
        // SAFETY: as for a shared borrow, the slot is the call's worker's,
        // aligned, holding a value and live for the call. The borrow is unique
        // while it lives: the context stays mutably borrowed for as long, and
        // every access to a slot, through this handle or any copy of any
        // handle, takes that context, so nothing else reaches the slot and no
        // shared borrow lent earlier is still alive. The context is the call's
        // own, so the borrow ends within the call, before the slot's
        // generation can be freed.
        unsafe { &mut *self.slot.as_ptr() }
    }
}

// SAFETY: the shared field is an offset and a count, which accept every bit
// pattern, with no padding and no drop; the values live in the slot block,
// which only the handles reach, each a worker's own slot, used only with that
// worker's call context, which is neither `Send` nor `Sync` and so never
// leaves the worker's thread.
unsafe impl<T: Copy + ShmLayout> Sync for PerWorker<T> {}

// SAFETY: as above; the block it names is owned by the graph and its slots
// are plain values aligned for `T` by the builder.
unsafe impl<T: Copy + ShmLayout> ShmLayout for PerWorker<T> {}

// The builder writes and frees the field through two raw words, the offset
// and then the count right after it, not through the struct, so its layout
// must stay what these assertions state.
const _: () = {
    assert!(size_of::<PerWorker<u64>>() == 16);
    assert!(core::mem::offset_of!(PerWorker<u64>, workers) == size_of::<isize>());
};
