//! Configuration field types backed by C registries of the module, and the
//! per-worker state of a configuration.

use core::marker::PhantomData;

use yanet_shm::{
    rel::RelPtr,
    shm::{MAX_ALIGN, ShmLayout},
};

use crate::dp::object::Object;

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
/// from one initial value.
///
/// A slot holds only plain values: a value that owns a block, names a registry
/// entry or embeds a C object is not `Copy`, so it cannot be a slot.
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
}

// SAFETY: the shared field is an offset and a count, which accept every bit
// pattern, with no padding and no drop; the slot block is written only by the
// build, before the value is shared.
unsafe impl<T: Copy + ShmLayout> Sync for PerWorker<T> {}

// SAFETY: as above; the block it names is owned by the graph and its slots
// are plain values aligned for `T` by the builder.
unsafe impl<T: Copy + ShmLayout> ShmLayout for PerWorker<T> {}

// The builder writes the offset word, then the count word right after it.
const _: () = {
    assert!(size_of::<PerWorker<u64>>() == 16);
    assert!(core::mem::offset_of!(PerWorker<u64>, workers) == size_of::<isize>());
};
