//! Configuration field types backed by C registries of the module.

use core::marker::PhantomData;

use yanet_shm::shm::ShmLayout;

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

// SAFETY: a registry index accepts every bit pattern, and the layout is a
// zero-sized marker; the control plane writes the id the module's counter
// registry assigned.
unsafe impl<T: CounterLayout> ShmLayout for Counter<T> {}

// SAFETY: a device index accepts every bit pattern; the control plane writes
// the index the module's device list assigned.
unsafe impl ShmLayout for Device {}
