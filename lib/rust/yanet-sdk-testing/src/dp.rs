//! Pure-Rust invocation harness of the dataplane runtime, Miri included.
//!
//! The invocation fakes what the dataplane hands a handler: the execution
//! context with its counter storage, device targets and prepared buffer, the
//! worker, and a front of packets backed by Rust buffers, all over a block
//! arena that stands for the shared mapping. A test lays a module's
//! configuration out by hand, as the control plane would, then runs the
//! commit pass and the handler and reads where every packet ended.

use alloc::{boxed::Box, vec, vec::Vec};
use core::{
    marker::PhantomData,
    mem::{align_of, offset_of, size_of},
    ptr::NonNull,
};

use yanet_shm::{rel::RelPtr, shm::ShmLayout};
use yanet_sys::{
    bindings,
    dp::{Batch, CommitCx, Cx, Module, ModuleConfig, rt},
};

use crate::BlockArena;

/// A module that does nothing and has the configuration `C`, the module of
/// the doc examples of the runtime.
#[doc(hidden)]
pub struct Model<C>(PhantomData<fn() -> C>);

impl<C: ShmLayout + Sync + 'static> Module for Model<C> {
    const NAME: &'static str = "mine";
    type Config = C;
    type Prepared<'p> = ();

    fn handle(_: &mut Cx<Self>, _: &mut Batch) {}

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
    // SAFETY: the word is aligned and lies in the configuration's block
    // (checked above); the write is raw. A handler that reads the
    // configuration afterwards relies on the contract of the invocation,
    // which forbids writes while it lives.
    unsafe { arena.root().ptr::<u64>(addr).write(value) }
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

/// Outcome of one handler call.
#[derive(Debug, PartialEq, Eq)]
pub struct Outcome {
    /// Fate of every packet, in input order.
    pub fates: Vec<Fate>,
    /// Counters of the front, as the handler left them.
    pub front: FrontCounters,
    /// Frame bytes of every packet after the call, in input order.
    pub frames: Vec<Vec<u8>>,
}

/// Mbuf and buffer of one harness input packet; the descriptor is at the
/// start of the buffer, in words so it is aligned.
type PacketStorage = (Box<bindings::rte_mbuf>, Box<[u64]>);

/// Fake dataplane state for calling a module's handler.
pub struct Invocation<M> {
    arena: &'static BlockArena,
    ectx: Box<bindings::module_ectx>,
    worker: Box<bindings::dp_worker>,
    counters: Vec<NonNull<u64>>,
    targets: NonNull<bindings::module_device_target>,
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
    /// number of counter slots, no device target and a prepared buffer of
    /// the module's type, which the commit pass fills here.
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
        // SAFETY: the bindgen structs are plain C data, valid when zeroed.
        let mut ectx: Box<bindings::module_ectx> = Box::new(unsafe { core::mem::zeroed() });
        // SAFETY: as above.
        let worker: Box<bindings::dp_worker> = Box::new(unsafe { core::mem::zeroed() });
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
            let value = alloc(size_of::<u64>()).cast::<u64>();
            let slot = table.as_ptr().addr() + idx * size_of::<RelPtr<u64>>();
            // SAFETY: the slot lies in the table block; an offset is written.
            unsafe {
                root.ptr::<isize>(slot)
                    .write(value.as_ptr().addr() as isize - slot as isize)
            };
            slots.push(value);
        }
        let targets = alloc(devices.max(1) * size_of::<bindings::module_device_target>())
            .cast::<bindings::module_device_target>();
        let prepared = (size_of::<M::Prepared<'static>>() != 0).then(|| alloc(size_of::<M::Prepared<'static>>()));
        // SAFETY: every pointer names a fresh block of the arena or the boxed
        // context; the written values are what the dataplane would store.
        unsafe {
            let handles = storage
                .as_ptr()
                .add(offset_of!(bindings::counter_storage, counter_value_handles));
            handles
                .cast::<isize>()
                .write(table.as_ptr().addr() as isize - handles.addr() as isize);
            for idx in 0..devices {
                (*targets.as_ptr().add(idx)).device_id = idx as u16;
            }
        }
        ectx.abs_cp_module = root.ptr(config);
        ectx.abs_counter_storage = storage.as_ptr().cast();
        ectx.device_target_count = devices as u64;
        ectx.abs_device_targets = targets.as_ptr();
        if let Some(prepared) = prepared {
            ectx.abs_module_prepared = prepared.as_ptr().cast();
        }
        // The commit pass fills the prepared buffer, as the dataplane does
        // when it publishes a generation, before any handler call.
        let raw = &raw mut *ectx;
        // SAFETY: the context is this harness's and its configuration a
        // published one of the module (constructor contract); the root
        // resolves every block of it.
        unsafe { rt::commit_with::<M>(root, raw, (*raw).abs_cp_module) };
        Self {
            arena,
            ectx,
            worker,
            counters: slots,
            targets,
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
        // SAFETY: the target table is a live arena block with one entry per
        // present device, and the index is below that count (checked above).
        unsafe { (*self.targets.as_ptr().add(idx)).device_id = u16::MAX };
    }

    /// Value of a counter slot.
    pub fn counter(&self, idx: usize) -> u64 {
        // SAFETY: the slot is a live arena block.
        unsafe { self.counters[idx].as_ptr().read() }
    }

    /// Calls the handler on the packets.
    ///
    /// # Panics
    ///
    /// When a packet's buffer does not fit the 16-bit mbuf geometry.
    pub fn run(&mut self, packets: Vec<TestPacket>) -> Outcome {
        for packet in &packets {
            let len = size_of::<bindings::packet>()
                + usize::from(packet.headroom)
                + packet.frame.len()
                + usize::from(packet.tailroom);
            assert!(
                len <= usize::from(u16::MAX),
                "the packet buffer exceeds the mbuf geometry"
            );
        }
        // Every box is in its final place before raw pointers are taken, so
        // no move invalidates them under the borrow models.
        let mut storage: Vec<PacketStorage> = packets
            .iter()
            .map(|packet| {
                // SAFETY: plain C data, valid when zeroed.
                let mbuf = Box::new(unsafe { core::mem::zeroed() });
                let start = size_of::<bindings::packet>() + usize::from(packet.headroom);
                let len = start + packet.frame.len() + usize::from(packet.tailroom);
                let mut buffer = vec![0u64; len.div_ceil(8)].into_boxed_slice();
                // SAFETY: the frame lies inside the buffer, which holds the
                // descriptor, the headroom, the frame and the tailroom.
                unsafe {
                    core::ptr::copy_nonoverlapping(
                        packet.frame.as_ptr(),
                        buffer.as_mut_ptr().cast::<u8>().add(start),
                        packet.frame.len(),
                    );
                }
                (mbuf, buffer)
            })
            .collect();
        let mut packet_ptrs: Vec<*mut bindings::packet> = Vec::new();
        for ((mbuf, buffer), packet) in storage.iter_mut().zip(&packets) {
            let mbuf_raw = &raw mut **mbuf;
            let base = buffer.as_mut_ptr().cast::<u8>();
            let packet_raw = base.cast::<bindings::packet>();
            packet_ptrs.push(packet_raw);
            let start = size_of::<bindings::packet>() + usize::from(packet.headroom);
            // SAFETY: the boxes are ours; the buffer outlives the call and
            // holds the descriptor at its start.
            unsafe {
                (*mbuf_raw).buf_addr = base.cast();
                (*mbuf_raw).buf_len = (start + packet.frame.len() + usize::from(packet.tailroom)) as u16;
                (*mbuf_raw).data_off = start as u16;
                (*mbuf_raw).data_len = packet.frame.len() as u16;
                (*mbuf_raw).pkt_len = packet.frame.len() as u32;
                (*packet_raw).mbuf = mbuf_raw;
                (*packet_raw).data_len = packet.frame.len() as u16;
                (*packet_raw).network_header.type_ = packet.network_type.to_be();
                (*packet_raw).network_header.offset = packet.network_offset;
                (*packet_raw).transport_header.type_ = packet.transport_type;
                (*packet_raw).transport_header.offset = packet.transport_offset;
            }
        }
        // SAFETY: plain C data, valid when zeroed.
        let mut front: Box<bindings::packet_front> = Box::new(unsafe { core::mem::zeroed() });
        let front_raw = &raw mut *front;
        // SAFETY: the front and the packets are exclusively ours and unlinked.
        // The loop is the C `packet_front_input`: it appends each packet to the
        // input list and keeps the input counters as the dataplane does.
        unsafe {
            for &packet in &packet_ptrs {
                (*packet).next = core::ptr::null_mut();
                let list = &raw mut (*front_raw).input;
                if (*list).last.is_null() {
                    (*list).last = &raw mut (*list).first;
                }
                *(*list).last = packet;
                (*list).last = &raw mut (*packet).next;
                (*front_raw).input_count += 1;
                (*front_raw).input_bytes += u64::from((*packet).data_len);
            }
        }
        // SAFETY: the context, worker and front are this harness's, the
        // configuration a published one of the module (constructor contract)
        // and committed at construction; the root resolves every arena block
        // the context names.
        unsafe { rt::invoke::<M>(self.arena.root(), &raw mut *self.worker, &raw mut *self.ectx, front_raw) };
        let mut fates = vec![None; packet_ptrs.len()];
        // SAFETY: the lists hold the harness's input packets only.
        let front_counters = unsafe {
            for (list, fate) in [
                (&raw mut (*front_raw).output, Fate::Output),
                (&raw mut (*front_raw).drop, Fate::Drop),
                (&raw mut (*front_raw).input, Fate::Input),
            ] {
                let mut cursor = (*list).first;
                while !cursor.is_null() {
                    let idx = packet_ptrs.iter().position(|&p| p == cursor).expect("foreign packet");
                    assert!(fates[idx].is_none(), "packet in two lists");
                    fates[idx] = Some(fate);
                    cursor = (*cursor).next;
                }
            }
            FrontCounters {
                input_count: (*front_raw).input_count,
                input_bytes: (*front_raw).input_bytes,
                output_count: (*front_raw).output_count,
                output_bytes: (*front_raw).output_bytes,
                drop_count: (*front_raw).drop_count,
                drop_bytes: (*front_raw).drop_bytes,
                pending_input_count: (*front_raw).pending_input_count,
                pending_input_bytes: (*front_raw).pending_input_bytes,
                pending_output_count: (*front_raw).pending_output_count,
                pending_output_bytes: (*front_raw).pending_output_bytes,
            }
        };
        let frames = packet_ptrs
            .iter()
            // SAFETY: the input packets are live harness storage.
            .map(|&packet| unsafe { current_frame(packet) })
            .collect();
        Outcome {
            fates: fates.into_iter().map(|fate| fate.unwrap_or(Fate::Lost)).collect(),
            front: front_counters,
            frames,
        }
    }
}

/// The frame of a harness packet as its mbuf describes it now.
///
/// # Safety
///
/// The packet must be a live harness packet.
unsafe fn current_frame(packet: *mut bindings::packet) -> Vec<u8> {
    // SAFETY: guaranteed by the caller; the mbuf geometry stays inside the
    // packet's buffer.
    unsafe {
        let mbuf = (*packet).mbuf;
        let start = (*mbuf).buf_addr.cast::<u8>().add(usize::from((*mbuf).data_off));
        core::slice::from_raw_parts(start, usize::from((*mbuf).data_len)).to_vec()
    }
}
