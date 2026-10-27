//! Fixture of the runtime reach test: only what the runtime reads, laid out
//! by hand, as the control plane would, in the block arena.
//!
//! It follows no packet rule of C and no rule of the dataplane.

use core::{
    alloc::Layout,
    marker::PhantomData,
    mem::{align_of, size_of, size_of_val},
    ptr::{self, NonNull},
};
use std::alloc::{alloc_zeroed, dealloc, handle_alloc_error};

use yanet_sdk_testing::BlockArena;
use yanet_sys::{
    bindings,
    dp::{Module, ModuleConfig, rt},
};

/// Writes `value` at `addr` of a block just allocated in `arena`.
///
/// # Safety
///
/// `addr` must lie in a block of `arena` that holds a `T` there, aligned and
/// referenced by nothing.
pub unsafe fn write<T>(arena: &'static BlockArena, addr: usize, value: T) {
    // SAFETY: the caller states that the block holds a `T` at the address.
    unsafe { arena.root().ptr::<T>(addr).write(value) }
}

/// Fake dataplane state for calling a module's handler.
pub struct Ectx<M> {
    pub arena: &'static BlockArena,
    /// Address of the execution context, which the entries take as the root
    /// of the mapping.
    ectx: usize,
    worker: bindings::dp_worker,
    _module: PhantomData<fn() -> M>,
}

impl<M: Module> Ectx<M> {
    /// Committed context of a module whose configuration starts with the
    /// words; `setup` fills the context before the commit, with the counter
    /// storage and the device targets the module reads.
    ///
    /// # Safety
    ///
    /// The words, laid out from the start of the configuration body, must
    /// form a valid `M::Config`, with zeros valid for the rest of it, and the
    /// counter ids in it must be below the number of slots `setup` provides.
    /// The handler must not redirect a packet: the targets have no routing
    /// state.
    pub unsafe fn new(config: &[u64], setup: impl FnOnce(&'static BlockArena, &mut bindings::module_ectx)) -> Self {
        const {
            assert!(
                align_of::<ModuleConfig<M::Config>>() <= 64,
                "the configuration is aligned beyond a block of the arena"
            )
        };
        // The checks a plugin export makes on the prepared value.
        yanet_sys::dp::__private::check_prepared::<M>();
        assert!(
            size_of_val(config) <= size_of::<M::Config>(),
            "the words exceed the body"
        );
        let arena = BlockArena::new();
        let root = arena.root();
        let alloc = |size: usize| arena.alloc(size).as_ptr().addr();
        let cp_module = alloc(size_of::<ModuleConfig<M::Config>>());
        // SAFETY: inside the configuration block just allocated, whose body
        // holds the words (size checked above).
        unsafe {
            root.ptr::<u64>(cp_module + ModuleConfig::<M::Config>::BODY_OFFSET)
                .copy_from_nonoverlapping(config.as_ptr(), config.len())
        };
        let mut context = bindings::module_ectx {
            abs_cp_module: root.ptr(cp_module),
            ..Default::default()
        };
        if size_of::<M::Prepared<'static>>() != 0 {
            context.abs_module_prepared = root.ptr(alloc(size_of::<M::Prepared<'static>>()));
        }
        setup(arena, &mut context);
        let ectx = alloc(size_of::<bindings::module_ectx>());
        // SAFETY: the context block was just allocated.
        unsafe { write(arena, ectx, context) };
        // SAFETY: the context lies in the arena, whose root it is, and its
        // configuration is a published one of the module (caller contract).
        unsafe { rt::commit_ectx::<M>(root.ptr(ectx), root.ptr(cp_module)) };
        Self {
            arena,
            ectx,
            worker: Default::default(),
            _module: PhantomData,
        }
    }

    /// Calls the handler on the packets as the input list of a front, and
    /// returns the front as the handler left it.
    ///
    /// The packets are borrowed mutably because the call rewrites their
    /// `next` links.
    pub fn run<'p>(&mut self, packets: &'p mut [Packet]) -> Front<'p> {
        let mut front = bindings::packet_front::default();
        if let (Some(first), Some(last)) = (packets.first(), packets.last()) {
            for pair in packets.windows(2) {
                // SAFETY: live buffers of the caller, read by nothing else now.
                unsafe { (*pair[0].packet()).next = pair[1].packet() };
            }
            // SAFETY: as above.
            let end = unsafe { &raw mut (*last.packet()).next };
            front.input = bindings::packet_list { first: first.packet(), last: end };
            front.input_count = packets.len() as u64;
            front.input_bytes = u64::from(FRAME) * packets.len() as u64;
        }
        // SAFETY: the context lies in the arena, whose root it is, committed
        // at construction; the worker, front and packets are live and ours.
        unsafe { rt::handle::<M>(&raw mut self.worker, self.arena.root().ptr(self.ectx), &raw mut front) };
        Front { raw: front, packets: &*packets }
    }
}

/// The front a handler left, tied to the packets its lists point into.
pub struct Front<'p> {
    raw: bindings::packet_front,
    packets: &'p [Packet],
}

impl Front<'_> {
    /// Indices into the input of the packets of the output list, in order.
    pub fn output(&self) -> Vec<usize> {
        self.indices(self.raw.output)
    }

    /// Indices into the input of the packets of the drop list, in order.
    pub fn dropped(&self) -> Vec<usize> {
        self.indices(self.raw.drop)
    }

    fn indices(&self, list: bindings::packet_list) -> Vec<usize> {
        let mut indices = Vec::new();
        let mut cursor = list.first;
        while !cursor.is_null() {
            indices.push(
                self.packets
                    .iter()
                    .position(|packet| ptr::eq(packet.packet(), cursor))
                    .expect("a packet of the input"),
            );
            // SAFETY: a live packet of the fixture, read by nothing else now.
            cursor = unsafe { (*cursor).next };
        }
        indices
    }
}

/// Size of the mbuf at the start of a packet allocation.
const MBUF: usize = size_of::<bindings::rte_mbuf>();

/// Bytes of the frame of every packet.
const FRAME: u16 = 60;

/// Offset of the frame in the buffer: the descriptor shares the buffer, so
/// the packet has no headroom past it.
const DATA_OFF: u16 = size_of::<bindings::packet>() as u16;

/// Layout of a packet allocation.
const LAYOUT: Layout =
    match Layout::from_size_align(MBUF + (DATA_OFF + FRAME) as usize, align_of::<bindings::rte_mbuf>()) {
        Ok(layout) => layout,
        Err(_) => panic!("the packet exceeds the address space"),
    };

/// A non-IP packet over one allocation: the mbuf, then the descriptor and
/// the frame, as the dataplane lays them out.
///
/// The allocation ends with the frame, so an SDK write past it leaves the
/// allocation and Miri reports it.
pub struct Packet {
    base: NonNull<u8>,
}

impl Packet {
    /// Packet whose frame is `first` repeated.
    fn new(first: u8) -> Self {
        // SAFETY: the layout has a non-zero size.
        let base = NonNull::new(unsafe { alloc_zeroed(LAYOUT) }).unwrap_or_else(|| handle_alloc_error(LAYOUT));
        // SAFETY: the mbuf and the descriptor are aligned and the frame lies
        // in the buffer, all inside the zeroed allocation.
        unsafe {
            let buffer = base.add(MBUF);
            let mbuf = bindings::rte_mbuf {
                buf_addr: buffer.as_ptr().cast(),
                buf_len: DATA_OFF + FRAME,
                data_off: DATA_OFF,
                data_len: FRAME,
                pkt_len: FRAME.into(),
                ..Default::default()
            };
            base.cast().write(mbuf);
            let packet = bindings::packet {
                mbuf: base.as_ptr().cast(),
                data_len: FRAME,
                ..Default::default()
            };
            buffer.cast().write(packet);
            buffer.add(DATA_OFF.into()).write_bytes(first, FRAME.into());
        }
        Self { base }
    }

    /// The descriptor, at the start of the buffer.
    fn packet(&self) -> *mut bindings::packet {
        // SAFETY: inside the allocation, where the descriptor was written.
        unsafe { self.base.add(MBUF) }.as_ptr().cast()
    }
}

impl Drop for Packet {
    fn drop(&mut self) {
        // SAFETY: allocated in `new` with this layout; no handler is running.
        unsafe { dealloc(self.base.as_ptr(), LAYOUT) }
    }
}

/// Packets of 60 bytes, the first byte of packet `n` being `n`.
pub fn numbered(count: usize) -> Vec<Packet> {
    (0..count).map(|idx| Packet::new(idx as u8)).collect()
}
