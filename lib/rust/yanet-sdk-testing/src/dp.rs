//! Pure-Rust invocation harness of the dataplane runtime, Miri included.
//!
//! The invocation fakes what the dataplane hands a handler: the execution
//! context with its counter storage, device targets and prepared buffer, the
//! worker, and a front of packets backed by Rust buffers, all over a block
//! arena that stands for the shared mapping. A test lays a module's
//! configuration out by hand, as the control plane would, then runs the
//! commit pass and the handler and reads where every packet ended.
//!
//! The worker carries a packet pool. The runtime calls the dataplane's
//! packet allocator and copy by their symbols, so a test binary whose
//! handler allocates or copies packets defines them over the pool with
//! [`define_pool_services!`](crate::define_pool_services).

use alloc::{
    alloc::{Layout, alloc_zeroed, handle_alloc_error},
    boxed::Box,
    vec,
    vec::Vec,
};
use core::{
    cell::RefCell,
    marker::PhantomData,
    mem::{align_of, offset_of, size_of},
    ptr::NonNull,
};

use yanet_shm::{rel::RelPtr, shm::ShmLayout};
#[doc(hidden)]
pub use yanet_sys::bindings;
use yanet_sys::dp::{Batch, CommitCx, Cx, Module, ModuleConfig, Verdict, rt};

use crate::BlockArena;

/// A module that passes every packet and has the configuration `C`, the
/// module of the doc examples of the runtime.
#[doc(hidden)]
pub struct Model<C>(PhantomData<fn() -> C>);

impl<C: ShmLayout + Sync + 'static> Module for Model<C> {
    const NAME: &'static str = "mine";
    type Config = C;
    type Prepared<'p> = ();

    fn handle(_: &mut Cx<Self>, batch: &mut Batch) {
        batch.dispatch(|_| Verdict::Pass);
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}

/// Allocates the zeroed block of a module configuration
/// and returns the address of its C header; the header bytes stay zero, as C
/// leaves them to itself.
pub fn alloc_config<B>(arena: &'static BlockArena) -> usize {
    arena.alloc(size_of::<ModuleConfig<B>>()).as_ptr().addr()
}

/// Panics unless the address starts a live arena block that holds a module
/// configuration of the body type.
fn assert_config_block<B>(arena: &BlockArena, config: usize) {
    let block = arena.block_of(config);
    assert!(
        matches!(block, Some((start, size)) if start == config && size >= size_of::<ModuleConfig<B>>()),
        "the address is not a configuration block of the arena"
    );
}

/// Writes an 8-byte word at an offset of the body of a configuration, as
/// the control plane would write a counter id, a device index
/// or a plain value.
///
/// # Panics
///
/// When the address is not a configuration block of the arena, or the word
/// is not an aligned word inside the body.
pub fn write_body_word<B>(arena: &'static BlockArena, config: usize, offset: usize, value: u64) {
    assert_config_block::<B>(arena, config);
    assert!(
        offset
            .checked_add(size_of::<u64>())
            .is_some_and(|end| end <= size_of::<B>()),
        "the word is outside the body"
    );
    let addr = config + ModuleConfig::<B>::BODY_OFFSET + offset;
    assert!(addr.is_multiple_of(align_of::<u64>()), "the word is misaligned");
    // A handler that reads the configuration afterwards relies on the
    // contract of the invocation, which forbids writes while it lives.
    arena.write(addr, value);
}

/// A packet handed to the harness: its frame bytes and the parser's header
/// offsets.
///
/// The packet descriptor shares the buffer with the room in front of the
/// frame, at its start, as the dataplane lays every packet out.
pub struct TestPacket {
    pub frame: Vec<u8>,
    /// Room in front of the frame beyond the descriptor.
    pub headroom: u16,
    pub tailroom: u16,
    /// Network type in host order.
    pub network_type: u16,
    pub network_offset: u16,
    pub transport_type: u16,
    pub transport_offset: u16,
}

impl TestPacket {
    /// Panics unless the buffer fits the 16-bit mbuf geometry and the header
    /// offsets are ones the parser could have recorded.
    ///
    /// The C packet routines trust them (attach: packet).
    fn assert_valid(&self) {
        const ETHER_LEN: usize = 14;
        const IPV4_MIN_LEN: usize = 20;
        const IPV6_LEN: usize = 40;
        const TCP_MIN_LEN: usize = 20;
        const UDP_LEN: usize = 8;
        let len =
            size_of::<bindings::packet>() + usize::from(self.headroom) + self.frame.len() + usize::from(self.tailroom);
        assert!(
            len <= usize::from(u16::MAX),
            "the packet buffer exceeds the mbuf geometry"
        );
        let network = usize::from(self.network_offset);
        if self.network_type != 0 || network != 0 {
            let header = match self.network_type {
                0x0800 => IPV4_MIN_LEN,
                0x86dd => IPV6_LEN,
                _ => 0,
            };
            assert!(
                network >= ETHER_LEN && network + header <= self.frame.len(),
                "the network header is not one the parser could have found"
            );
        }
        let transport = usize::from(self.transport_offset);
        if self.transport_type != 0 || transport != 0 {
            let available = u32::from(self.transport_type) & bindings::PACKET_TRANSPORT_HEADER_UNAVAILABLE == 0;
            let header = match (available, self.transport_type & 0xff) {
                (true, 6) => TCP_MIN_LEN,
                (true, 17) => UDP_LEN,
                _ => 0,
            };
            assert!(
                transport >= network && transport + header <= self.frame.len(),
                "the transport header is not one the parser could have found"
            );
        }
    }

    /// Packet whose network header starts after a 14-byte Ethernet header.
    pub fn ether(network_type: u16, frame: Vec<u8>) -> Self {
        Self {
            frame,
            headroom: 0,
            tailroom: 0,
            network_type,
            network_offset: 14,
            transport_type: 0,
            transport_offset: 0,
        }
    }
}

/// Where a packet ended after a call.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Fate {
    Output,
    Drop,
    /// Still on the input list, against the drain obligation of a module.
    Input,
    /// On no list of the front: taken and never disposed of.
    Lost,
}

/// Counters of the front after a call.
#[derive(Debug, Default, Clone, Copy, PartialEq, Eq)]
pub struct FrontCounters {
    pub input_count: u64,
    pub input_bytes: u64,
    pub output_count: u64,
    pub output_bytes: u64,
    pub drop_count: u64,
    pub drop_bytes: u64,
    pub pending_input_count: u64,
    pub pending_input_bytes: u64,
    pub pending_output_count: u64,
    pub pending_output_bytes: u64,
}

/// A packet's frame after a call: its bytes and what describes them.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Frame {
    pub bytes: Vec<u8>,
    pub network_offset: u16,
    pub transport_offset: u16,
    /// Packet length the mbuf records.
    pub pkt_len: u32,
}

/// Outcome of one handler call.
#[derive(Debug, PartialEq, Eq)]
pub struct Outcome {
    /// Fate of every packet, in input order.
    pub fates: Vec<Fate>,
    /// Counters of the front, as the handler left them.
    pub front: FrontCounters,
    /// Frame of every packet after the call, in input order.
    pub frames: Vec<Frame>,
    /// Packets the handler allocated or copied, with their fate, in list
    /// order (output, drop, then input).
    ///
    /// One on no list, or redirected to a device, is not found and stays
    /// outstanding until the pool drops.
    pub generated: Vec<(Fate, Frame)>,
}

/// Buffer size of a packet the harness pool allocates, descriptor and
/// headroom included.
///
/// The data room of the dataplane's worker pools: the element size
/// `MBUF_MAX_SIZE` less the mbuf header.
pub const POOL_BUFFER: usize = bindings::MBUF_MAX_SIZE as usize - size_of::<bindings::rte_mbuf>();

/// Headroom of a packet the harness pool allocates, the descriptor at the
/// start of the buffer included.
///
/// DPDK's `RTE_PKTMBUF_HEADROOM`, as the dataplane's pools are built.
pub const POOL_HEADROOM: u16 = bindings::RTE_PKTMBUF_HEADROOM as u16;

/// Bytes a front prepend may take on an empty pool packet: the headroom
/// beyond the descriptor.
pub const POOL_PREPEND_ROOM: usize = POOL_HEADROOM as usize - size_of::<bindings::packet>();

// The mbuf geometry is 16-bit.
const _: () = assert!(POOL_BUFFER <= u16::MAX as usize && bindings::RTE_PKTMBUF_HEADROOM <= u16::MAX as u32);

/// Size of the packet descriptor at the start of every packet buffer.
const DESCRIPTOR: usize = size_of::<bindings::packet>();

// The list end of a packet is the address of its first field.
const _: () = assert!(offset_of!(bindings::packet, next) == 0);

/// Buffer of a packet: the descriptor at its start, then the headroom, the
/// frame and the tailroom, as the dataplane lays every packet out.
///
/// It is exactly as long as its mbuf reports, so an SDK write past that
/// length leaves the allocation and Miri flags it, where spare room would
/// hide it; only the padding to the descriptor's alignment can absorb one.
#[repr(C)]
struct Buffer<T: ?Sized = [u8]> {
    packet: bindings::packet,
    bytes: T,
}

impl Buffer {
    /// A zeroed buffer of the descriptor and `len` bytes after it, no more.
    fn zeroed(len: usize) -> Box<Self> {
        let (layout, _) = Layout::new::<bindings::packet>()
            .extend(Layout::array::<u8>(len).expect("the buffer exceeds the address space"))
            .expect("the buffer exceeds the address space");
        let layout = layout.pad_to_align();
        // SAFETY: the layout is not zero-sized, the descriptor being part of
        // it.
        let raw = unsafe { alloc_zeroed(layout) };
        if raw.is_null() {
            handle_alloc_error(layout);
        }
        // SAFETY: a fresh zeroed allocation that the box frees with the
        // result's own layout.
        //
        // Its layout is `Layout::for_value` of the result: the bytes follow
        // the descriptor at the descriptor's size, and the total is rounded up
        // to the descriptor's alignment. Zero bytes are a valid descriptor,
        // which holds only integers and pointers, and valid bytes.
        unsafe { Box::from_raw(core::ptr::slice_from_raw_parts_mut(raw, len) as *mut Self) }
    }
}

/// Mbuf and buffer of one packet, leaked from boxes until reclaimed.
///
/// The two point at each other, as the dataplane's allocator lays them out:
/// the mbuf's buffer address at the descriptor, the descriptor's mbuf at the
/// mbuf. They are leaked so that no move of a box invalidates the pointers
/// the handler holds.
struct Parts {
    mbuf: NonNull<bindings::rte_mbuf>,
    buffer: NonNull<Buffer>,
}

impl Parts {
    /// Packet with the descriptor, whose mbuf link is set here, and the
    /// frame at `data_off` of a buffer of `buf_len` bytes.
    ///
    /// Panics when the frame does not fit the buffer.
    fn new(mut descriptor: bindings::packet, data_off: u16, buf_len: u16, frame: &[u8]) -> Self {
        let len = u16::try_from(frame.len()).expect("the frame exceeds the mbuf geometry");
        let start = usize::from(data_off) - DESCRIPTOR;
        descriptor.data_len = len;
        let mut buffer = Buffer::zeroed(usize::from(buf_len) - DESCRIPTOR);
        buffer.packet = descriptor;
        buffer.bytes[start..start + frame.len()].copy_from_slice(frame);
        let mbuf = Box::new(bindings::rte_mbuf {
            buf_len,
            data_off,
            data_len: len,
            pkt_len: u32::from(len),
            ..Default::default()
        });
        // Only the mbuf is written through a reference once the buffer's
        // pointer exists; the buffer's one later write uses that pointer.
        let buffer = NonNull::from(Box::leak(buffer));
        let mbuf = Box::leak(mbuf);
        mbuf.buf_addr = buffer.as_ptr().cast();
        let mbuf = NonNull::from(mbuf);
        // SAFETY: the buffer is a live allocation of ours that no reference
        // aliases; the write goes through its own pointer.
        unsafe { (*buffer.as_ptr()).packet.mbuf = mbuf.as_ptr() };
        Self { mbuf, buffer }
    }

    /// The descriptor, at the start of the buffer.
    fn packet(&self) -> *mut bindings::packet {
        self.buffer.as_ptr().cast()
    }

    /// The `next` field of the descriptor, the end of a list that ends here.
    fn tail(&self) -> *mut *mut bindings::packet {
        self.buffer.as_ptr().cast()
    }

    /// Takes the boxes back.
    ///
    /// # Safety
    ///
    /// No handler call that could reach the packet is running, and no pointer
    /// into it is used afterwards.
    unsafe fn reclaim(self) -> (Box<bindings::rte_mbuf>, Box<Buffer>) {
        // SAFETY: both boxes were leaked at construction, `self` is their only
        // owner, and nothing else uses them any more (caller contract).
        unsafe { (Box::from_raw(self.mbuf.as_ptr()), Box::from_raw(self.buffer.as_ptr())) }
    }
}

/// Packet pool of a harness worker, found through the worker's pool
/// pointer as the dataplane's allocator finds its mempool.
struct Pool {
    capacity: usize,
    live: RefCell<Vec<Parts>>,
}

impl Drop for Pool {
    fn drop(&mut self) {
        for packet in self.live.get_mut().drain(..) {
            // SAFETY: the pool drops with its invocation, after every call.
            drop(unsafe { packet.reclaim() });
        }
    }
}

impl Pool {
    /// A pool packet with the descriptor and the frame after the pool's
    /// headroom, null when the pool is exhausted.
    fn add(&self, descriptor: bindings::packet, frame: &[u8]) -> *mut bindings::packet {
        let mut live = self.live.borrow_mut();
        if live.len() >= self.capacity {
            return core::ptr::null_mut();
        }
        let parts = Parts::new(descriptor, POOL_HEADROOM, POOL_BUFFER as u16, frame);
        let packet = parts.packet();
        live.push(parts);
        packet
    }
}

/// The pool of a harness worker.
///
/// # Safety
///
/// The worker must be a harness worker in a handler call, whose pool pointer
/// names its pool.
unsafe fn pool_of<'a>(worker: *mut bindings::dp_worker) -> &'a Pool {
    // SAFETY: the harness stores its pool in the worker for the call
    // (function contract).
    unsafe { &*(*worker).rx_mempool.cast::<Pool>() }
}

/// The harness allocator, as `worker_packet_alloc`: a packet with zeroed
/// metadata, null when the pool is exhausted.
///
/// The frame starts empty after the pool's headroom.
///
/// # Safety
///
/// The worker must be a harness worker in a handler call, whose pool
/// pointer names its pool.
pub unsafe fn pool_alloc(worker: *mut bindings::dp_worker) -> *mut bindings::packet {
    // SAFETY: function contract.
    unsafe { pool_of(worker) }.add(bindings::packet::default(), &[])
}

/// The harness copy: a pool packet with the source's metadata and a copy of
/// its frame after the pool's headroom; null when it cannot be made.
///
/// The recirculation budget is split as `worker_clone_packet` splits it. Null
/// means the pool is exhausted or the frame does not fit a pool buffer, where
/// `rte_pktmbuf_copy` would chain segments.
///
/// # Safety
///
/// As for [`pool_alloc`]; the packet must be a valid, exclusively owned
/// harness or pool packet.
pub unsafe fn pool_clone(
    worker: *mut bindings::dp_worker,
    packet: *mut bindings::packet,
    recirc_limit: u16,
) -> *mut bindings::packet {
    // SAFETY: function contract.
    let pool = unsafe { pool_of(worker) };
    // SAFETY: the packet is valid (function contract); its mbuf describes
    // frame bytes inside its buffer.
    let (mut source, frame) = unsafe {
        let source = *packet;
        let mbuf = &*source.mbuf;
        let start = mbuf.buf_addr.cast::<u8>().add(usize::from(mbuf.data_off));
        (
            source,
            core::slice::from_raw_parts(start, usize::from(mbuf.data_len)).to_vec(),
        )
    };
    if frame.len() > POOL_BUFFER - usize::from(POOL_HEADROOM) || pool.live.borrow().len() >= pool.capacity {
        return core::ptr::null_mut();
    }
    // `packet_recirc_init`, lib/dataplane/packet/packet.h.
    if source.recirc_initialized == 0 {
        source.recirc_remaining = recirc_limit;
        source.recirc_initialized = 1;
    }
    let clone = bindings::packet {
        next: core::ptr::null_mut(),
        recirc_remaining: source.recirc_remaining / 2,
        ..source
    };
    source.recirc_remaining -= clone.recirc_remaining;
    // SAFETY: the packet is valid and exclusively ours (function contract).
    unsafe { *packet = source };
    pool.add(clone, &frame)
}

/// Defines the host dataplane's packet allocator and copy over the harness
/// pool, in the crate of a test binary.
///
/// The runtime calls the C allocator by its symbol, so a binary that runs a
/// handler which allocates or copies packets against an [`Invocation`]
/// defines the symbols itself, once; no build of a product expands this
/// macro, so none contains the pool.
#[macro_export]
macro_rules! define_pool_services {
    () => {
        #[unsafe(no_mangle)]
        unsafe extern "C" fn worker_packet_alloc(
            worker: *mut $crate::dp::bindings::dp_worker,
        ) -> *mut $crate::dp::bindings::packet {
            // SAFETY: the harness worker of the running call stores its pool.
            unsafe { $crate::dp::pool_alloc(worker) }
        }

        #[unsafe(no_mangle)]
        unsafe extern "C" fn worker_clone_packet(
            worker: *mut $crate::dp::bindings::dp_worker,
            packet: *mut $crate::dp::bindings::packet,
            packet_recirc_limit: u16,
        ) -> *mut $crate::dp::bindings::packet {
            // SAFETY: the harness worker and an owned packet of the running
            // call.
            unsafe { $crate::dp::pool_clone(worker, packet, packet_recirc_limit) }
        }
    };
}

/// Fake dataplane state for calling a module's handler.
pub struct Invocation<M> {
    arena: &'static BlockArena,
    ectx: Box<bindings::module_ectx>,
    worker: Box<bindings::dp_worker>,
    pool: Box<Pool>,
    /// Arena addresses of the counter values.
    counters: Vec<usize>,
    /// Arena address of the device target table.
    targets: usize,
    devices: usize,
    /// Arena blocks of the fake context, freed with the invocation.
    blocks: Vec<usize>,
    _module: PhantomData<fn() -> M>,
}

impl<M> Drop for Invocation<M> {
    fn drop(&mut self) {
        for &block in &self.blocks {
            self.arena.free(block);
        }
    }
}

impl<M: Module> Invocation<M> {
    /// Context for the configuration at an address of the arena, with a
    /// number of counter slots and no device target.
    ///
    /// It has a prepared buffer of the module's type, which the commit pass
    /// fills here, and a pool of 64 packets.
    ///
    /// # Safety
    ///
    /// The configuration is what the runtime trusts a published one to be
    /// (attach: binding, frozen, reclaim): a laid-out body of the module's
    /// configuration type whose counter ids are below the number of counter
    /// slots and whose edges name live blocks of their targets; it and those
    /// blocks stay live and are not written while the invocation lives.
    ///
    /// # Panics
    ///
    /// When the address is not a configuration block of the module's body
    /// type in the arena.
    pub unsafe fn new(arena: &'static BlockArena, config: usize, counters: usize) -> Self {
        Self::build(arena, config, counters, 0)
    }

    /// As [`Invocation::new`], with a number of present device targets the
    /// handler can resolve.
    ///
    /// # Safety
    ///
    /// As for [`Invocation::new`]; and the handler must not redirect a
    /// packet: the targets have no device entries or routing state, so the C
    /// routing would read null entries.
    pub unsafe fn with_devices(arena: &'static BlockArena, config: usize, counters: usize, devices: usize) -> Self {
        Self::build(arena, config, counters, devices)
    }

    fn build(arena: &'static BlockArena, config: usize, counters: usize, devices: usize) -> Self {
        // The checks `export_module!` makes on the prepared value, which the
        // buffer below relies on as the dataplane's does.
        yanet_sys::dp::__private::check_prepared::<M>();
        assert_config_block::<M::Config>(arena, config);
        let mut ectx = Box::new(bindings::module_ectx::default());
        let worker = Box::new(bindings::dp_worker::default());
        let pool = Box::new(Pool {
            capacity: 64,
            live: RefCell::new(Vec::new()),
        });
        let root = arena.root();

        let mut blocks = Vec::new();
        let mut alloc = |size: usize| {
            let block = arena.alloc(size);
            blocks.push(block.as_ptr().addr());
            block
        };
        let storage = alloc(size_of::<bindings::counter_storage>());
        let table = alloc(counters.max(1) * size_of::<RelPtr<u64>>());
        let mut slots = Vec::new();
        for idx in 0..counters {
            let value = alloc(size_of::<u64>()).as_ptr().addr();
            let slot = table.as_ptr().addr() + idx * size_of::<RelPtr<u64>>();
            // A self-relative slot stores the offset to its target.
            arena.write(slot, value as isize - slot as isize);
            slots.push(value);
        }
        let targets = alloc(devices.max(1) * size_of::<bindings::module_device_target>())
            .cast::<bindings::module_device_target>();
        let targets_addr = targets.as_ptr().addr();
        for idx in 0..devices {
            let target = bindings::module_device_target {
                device_id: idx as u16,
                ..Default::default()
            };
            arena.write(targets_addr + idx * size_of::<bindings::module_device_target>(), target);
        }
        let prepared = (size_of::<M::Prepared<'static>>() != 0).then(|| alloc(size_of::<M::Prepared<'static>>()));
        let handles = storage.as_ptr().addr() + offset_of!(bindings::counter_storage, counter_value_handles);
        arena.write(handles, table.as_ptr().addr() as isize - handles as isize);
        ectx.abs_cp_module = root.ptr(config);
        ectx.abs_counter_storage = storage.as_ptr().cast();
        ectx.device_target_count = devices as u64;
        ectx.abs_device_targets = targets.as_ptr();
        if let Some(prepared) = prepared {
            ectx.abs_module_prepared = prepared.as_ptr().cast();
        }
        // The commit pass fills the prepared buffer, as the dataplane does
        // when it publishes a generation, before any handler call.
        let cp_module = ectx.abs_cp_module;
        let raw = &raw mut *ectx;
        // SAFETY: the context is this harness's and its configuration a
        // published one of the module (constructor contract); the root
        // resolves every block of it.
        unsafe { rt::commit_with::<M>(root, raw, cp_module) };
        Self {
            arena,
            ectx,
            worker,
            pool,
            counters: slots,
            targets: targets_addr,
            devices,
            blocks,
            _module: PhantomData,
        }
    }

    /// Sets the worker index and the round time the handler sees.
    pub fn set_worker(&mut self, idx: u64, now: u64) {
        self.worker.idx = idx;
        self.worker.current_time = now;
    }

    /// Makes a device target absent from the generation, as the
    /// dataplane marks one with the invalid device id.
    ///
    /// # Panics
    ///
    /// When the index is not one of a present device.
    pub fn set_device_absent(&mut self, idx: usize) {
        assert!(idx < self.devices, "no device target at this index");
        // The other fields of a target are zero, as construction left them.
        let target = bindings::module_device_target {
            device_id: u16::MAX,
            ..Default::default()
        };
        self.arena
            .write(self.targets + idx * size_of::<bindings::module_device_target>(), target);
    }

    /// Limits the worker's packet pool to a number of packets out at once.
    pub fn set_pool_capacity(&mut self, capacity: usize) {
        self.pool.capacity = capacity;
    }

    /// Pool packets allocated and not yet returned: those the handler left
    /// on no list.
    pub fn outstanding(&self) -> usize {
        self.pool.live.borrow().len()
    }

    /// Value of a counter slot.
    pub fn counter(&self, idx: usize) -> u64 {
        self.arena.read_word(self.counters[idx])
    }

    /// Calls the handler on the packets.
    ///
    /// Packets the handler allocated or copied are collected from the lists,
    /// recorded and returned to the pool.
    ///
    /// # Panics
    ///
    /// When a packet's buffer does not fit the 16-bit mbuf geometry, or its
    /// header offsets are ones the dataplane parser never records.
    pub fn run(&mut self, packets: Vec<TestPacket>) -> Outcome {
        for packet in &packets {
            packet.assert_valid();
        }
        // The packets are built last to first, so each descriptor holds the
        // link to its successor from the start and no box is written after the
        // handler's pointers into it exist.
        let mut next = core::ptr::null_mut();
        let mut tail = None;
        let mut storage: Vec<Parts> = Vec::with_capacity(packets.len());
        for packet in packets.iter().rev() {
            let start = DESCRIPTOR + usize::from(packet.headroom);
            let descriptor = bindings::packet {
                next,
                network_header: bindings::network_header {
                    type_: packet.network_type.to_be(),
                    offset: packet.network_offset,
                },
                transport_header: bindings::transport_header {
                    type_: packet.transport_type,
                    offset: packet.transport_offset,
                },
                ..Default::default()
            };
            let buf_len = start + packet.frame.len() + usize::from(packet.tailroom);
            let parts = Parts::new(descriptor, start as u16, buf_len as u16, &packet.frame);
            next = parts.packet();
            tail.get_or_insert_with(|| parts.tail());
            storage.push(parts);
        }
        storage.reverse();
        let packet_ptrs: Vec<*mut bindings::packet> = storage.iter().map(Parts::packet).collect();
        // The front holds the input list the dataplane's own append would have
        // built, with the input counters it keeps.
        let mut front = Box::new(bindings::packet_front::default());
        if let Some(last) = tail {
            front.input = bindings::packet_list { first: next, last };
            front.input_count = packets.len() as u64;
            front.input_bytes = packets.iter().map(|packet| packet.frame.len() as u64).sum();
        }
        // The pool pointer is taken for this call only, so no later
        // exclusive borrow of the pool outlives it.
        self.worker.rx_mempool = core::ptr::from_ref(&*self.pool).cast_mut().cast();
        // SAFETY: the context, worker and front are this harness's, the
        // configuration a published one of the module (constructor contract)
        // and committed at construction; the root resolves every arena block
        // the context names, and the test binary defines the packet services
        // over this worker's pool.
        unsafe {
            rt::invoke::<M>(
                self.arena.root(),
                &raw mut *self.worker,
                &raw mut *self.ectx,
                &raw mut *front,
            )
        };
        // The call is over: the harness owns every packet again, and walks
        // the lists through their boxes.
        // SAFETY: the call is over, and the walk below reads the packets
        // through their boxes only.
        let inputs: Vec<_> = storage.into_iter().map(|parts| unsafe { parts.reclaim() }).collect();
        let mut fates = vec![None; packet_ptrs.len()];
        let mut generated = Vec::new();
        for (first, fate) in [
            (front.output.first, Fate::Output),
            (front.drop.first, Fate::Drop),
            (front.input.first, Fate::Input),
        ] {
            let mut cursor = first;
            while !cursor.is_null() {
                if let Some(idx) = packet_ptrs.iter().position(|&p| p == cursor) {
                    assert!(fates[idx].is_none(), "packet in two lists");
                    fates[idx] = Some(fate);
                    cursor = inputs[idx].1.packet.next;
                } else {
                    let pooled = {
                        let mut live = self.pool.live.borrow_mut();
                        let idx = live.iter().position(|p| p.packet() == cursor).expect("foreign packet");
                        live.swap_remove(idx)
                    };
                    // SAFETY: as for the input packets.
                    let (mbuf, buffer) = unsafe { pooled.reclaim() };
                    generated.push((fate, frame(&mbuf, &buffer)));
                    cursor = buffer.packet.next;
                }
            }
        }
        let front_counters = FrontCounters {
            input_count: front.input_count,
            input_bytes: front.input_bytes,
            output_count: front.output_count,
            output_bytes: front.output_bytes,
            drop_count: front.drop_count,
            drop_bytes: front.drop_bytes,
            pending_input_count: front.pending_input_count,
            pending_input_bytes: front.pending_input_bytes,
            pending_output_count: front.pending_output_count,
            pending_output_bytes: front.pending_output_bytes,
        };
        let frames = inputs.iter().map(|(mbuf, buffer)| frame(mbuf, buffer)).collect();
        Outcome {
            fates: fates.into_iter().map(|fate| fate.unwrap_or(Fate::Lost)).collect(),
            front: front_counters,
            frames,
            generated,
        }
    }
}

/// The frame of a packet as its mbuf and descriptor describe it now.
fn frame(mbuf: &bindings::rte_mbuf, buffer: &Buffer) -> Frame {
    let start = usize::from(mbuf.data_off) - DESCRIPTOR;
    Frame {
        bytes: buffer.bytes[start..start + usize::from(mbuf.data_len)].to_vec(),
        network_offset: buffer.packet.network_header.offset,
        transport_offset: buffer.packet.transport_header.offset,
        pkt_len: mbuf.pkt_len,
    }
}
