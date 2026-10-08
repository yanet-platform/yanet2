//! Byte-exact mirrors of the dataplane ABI structs.
//!
//! Every layout here is pinned by `lib/dataplane/config/plugin_abi_assert.h`
//! on the C side and by the const asserts below on this side; the
//! dataplane_ut suite additionally cross-checks sizes and offsets against
//! the real headers in the same binary, so a divergence fails a build or
//! a test instead of corrupting shared memory.
//!
//! Runtime-only fields (packet lists, `abs_*` addresses, worker-local
//! pointers) are raw pointers because only the dataplane process touches
//! them; control-plane-owned links stay offset pointers.

use core::ffi::c_void;

use yanet_shm::{CounterStorage, CpModule, OffsetPtr};

/// Dataplane <-> module ABI version this SDK compiles against.
///
/// Mirrors `YANET_MODULE_ABI_VERSION` in `lib/dataplane/module/module.h`;
/// bump both together, in the change that alters any mirrored layout.
pub const YANET_MODULE_ABI_VERSION: u32 = 33;

/// Symbol name a module `.so` exports carrying its ABI version.
pub const YANET_MODULE_ABI_VERSION_SYMBOL: &str = "yanet_module_abi_version";

// ---------------------------------------------------------------------------
// module descriptor
// ---------------------------------------------------------------------------

/// Handler entry: process the front's input into output or drop.
pub type ModuleHandler = unsafe extern "C" fn(*mut DpWorker, *mut ModuleEctx, *mut PacketFront);

/// Commit entry: generation-invariant derivation, once per generation.
pub type ModuleCommitHandler = unsafe extern "C" fn(*mut DpConfig, *mut CpModule);

/// Per-worker commit entry: fills the module's private prepared buffer.
pub type ModuleCommitEctxHandler = unsafe extern "C" fn(*mut ModuleEctx, *mut CpModule);

/// Mirror of `struct module` (`lib/dataplane/module/module.h`).
#[repr(C)]
pub struct Module {
    pub name: [u8; MODULE_TYPE_LEN],
    pub handler: Option<ModuleHandler>,
    pub commit_handler: Option<ModuleCommitHandler>,
    pub commit_ectx_handler: Option<ModuleCommitEctxHandler>,
    pub prepared_size: u64,
}

/// Capacity of the module type name, counting the terminating zero.
pub const MODULE_TYPE_LEN: usize = 80;

// ---------------------------------------------------------------------------
// packet
// ---------------------------------------------------------------------------

/// Marks a transport type whose header was not parsed (fragment payload).
pub const PACKET_TRANSPORT_HEADER_UNAVAILABLE: u16 = 0x100;

/// Default redirect budget of a packet lineage.
pub const PACKET_RECIRC_LIMIT_DEFAULT: u16 = 64;

/// Bit set in `Packet::flags` for a fragmented packet.
pub const PACKET_FLAG_FRAGMENTED: u16 = 1 << 0;

/// Bit set in `Packet::flags` for a locally emitted fwstate sync packet.
pub const PACKET_FLAG_FWSTATE_SYNC_INTERNAL: u16 = 1 << 1;

#[repr(C)]
struct NetworkHeaderRaw {
    type_: u16,
    offset: u16,
}

#[repr(C)]
struct TransportHeaderRaw {
    type_: u16,
    offset: u16,
}

/// Mirror of `struct packet` (`lib/dataplane/packet/packet.h`).
///
/// The descriptor lives in the mbuf headroom: `mbuf_to_packet` is just
/// `mbuf.buf_addr`, which is why every mirror keeps the exact 48-byte
/// layout.
#[repr(C)]
pub struct Packet {
    pub next: *mut Packet,
    pub mbuf: *mut RteMbuf,
    pub hash: u32,
    pub rx_device_id: u16,
    pub tx_device_id: u16,
    pub flags: u16,
    pub vlan: u16,
    pub recirc_remaining: u16,
    pub recirc_initialized: u8,
    _pad0: [u8; 1],
    pub flow_label: u32,
    pub fragment_offset: u16,
    pub data_len: u16,
    network_header: NetworkHeaderRaw,
    transport_header: TransportHeaderRaw,
}

impl Packet {
    /// Network header type in host byte order of the wire value (an
    /// EtherType compared against `to_be` constants).
    pub fn network_type(&self) -> u16 {
        self.network_header.type_
    }

    /// Byte offset of the network header inside the packet data.
    pub fn network_offset(&self) -> u16 {
        self.network_header.offset
    }

    /// Transport header type: protocol plus tag bits like
    /// [`PACKET_TRANSPORT_HEADER_UNAVAILABLE`].
    pub fn transport_type(&self) -> u16 {
        self.transport_header.type_
    }

    /// Byte offset of the transport header inside the packet data.
    pub fn transport_offset(&self) -> u16 {
        self.transport_header.offset
    }

    /// The declared transport protocol, meaningful even when the header
    /// itself is unavailable inside a non-initial fragment.
    pub fn transport_protocol(&self) -> u8 {
        self.transport_header.type_ as u8
    }

    pub(crate) fn set_network_offset(&mut self, offset: u16) {
        self.network_header.offset = offset;
    }

    pub(crate) fn set_transport(&mut self, type_: u16, offset: u16) {
        self.transport_header.type_ = type_;
        self.transport_header.offset = offset;
    }
}

/// Mirror of `struct packet_list` (`lib/dataplane/packet/packet.h`).
#[repr(C)]
pub struct PacketList {
    pub first: *mut Packet,
    /// Address of the last link: either `&first` of a fresh list or the
    /// `next` field of the tail packet.
    pub last: *mut *mut Packet,
}

/// Mirror of `struct packet_front`
/// (`lib/dataplane/module/packet_front.h`).
#[repr(C)]
pub struct PacketFront {
    pub input: PacketList,
    pub output: PacketList,
    pub drop: PacketList,
    pub pending_input_count: u64,
    pub pending_input_bytes: u64,
    pub pending_output_count: u64,
    pub pending_output_bytes: u64,
    pub input_count: u64,
    pub input_bytes: u64,
    pub output_count: u64,
    pub output_bytes: u64,
    pub drop_count: u64,
    pub drop_bytes: u64,
}

// ---------------------------------------------------------------------------
// mbuf
// ---------------------------------------------------------------------------

/// Mirror of `struct rte_mbuf` for the standard build (IOVA in mbuf).
///
/// Only the fields the packet path reads are named; the layout keeps the
/// DPDK cacheline shape so the C cross-check test can pin offsets.
#[repr(C, align(64))]
pub struct RteMbuf {
    pub buf_addr: *mut c_void,
    pub buf_iova: u64,
    pub data_off: u16,
    pub refcnt: u16,
    pub nb_segs: u16,
    pub port: u16,
    pub ol_flags: u64,
    pub packet_type: u32,
    pub pkt_len: u32,
    pub data_len: u16,
    pub vlan_tci: u16,
    pub hash_rss: u32,
    pub hash_fdir_hi: u32,
    pub vlan_tci_outer: u16,
    pub buf_len: u16,
    pub pool: *mut c_void,
    pub next: *mut RteMbuf,
    pub tx_offload: u64,
    pub shinfo: *mut c_void,
    pub priv_size: u16,
    pub timesync: u16,
    pub dynfield1: [u32; 9],
}

// ---------------------------------------------------------------------------
// execution context
// ---------------------------------------------------------------------------

/// Mirror of `struct module_device_target`
/// (`lib/dataplane/pipeline/econtext.h`).
#[repr(C)]
pub struct ModuleDeviceTarget {
    pub device_id: u16,
    _pad0: [u8; 6],
    pub input_entry: OffsetPtr<DeviceEntryEctx>,
    pub output_entry: OffsetPtr<DeviceEntryEctx>,
    pub abs_input_entry: *mut DeviceEntryEctx,
    pub abs_output_entry: *mut DeviceEntryEctx,
}

/// Mirror of `struct module_object_link_ectx`.
#[repr(C)]
pub struct ModuleObjectLinkEctx {
    pub counter_storage: *mut CounterStorage,
    pub object_ectx: OffsetPtr<ObjectEctx>,
    pub abs_object_ectx: *mut ObjectEctx,
}

/// Mirror of `struct object_ectx`.
#[repr(C)]
pub struct ObjectEctx {
    pub cp_object: OffsetPtr<yanet_shm::CpObject>,
    pub abs_cp_object: *mut yanet_shm::CpObject,
    pub counter_storage: *mut CounterStorage,
}

/// Mirror of `struct module_ectx`
/// (`lib/dataplane/pipeline/econtext.h`).
#[repr(C)]
pub struct ModuleEctx {
    pub handler: Option<ModuleHandler>,
    pub cp_module: OffsetPtr<CpModule>,
    pub abs_cp_module: *mut CpModule,
    pub rx_counter: *mut u64,
    pub tx_counter: *mut u64,
    pub drop_counter: *mut u64,
    pub pending_input_counter: *mut u64,
    pub pending_output_counter: *mut u64,
    pub counter_storage: OffsetPtr<CounterStorage>,
    pub abs_counter_storage: *mut CounterStorage,
    pub runtime_counter_storage_count: u64,
    pub runtime_counter_storages: OffsetPtr<OffsetPtr<CounterStorage>>,
    pub abs_runtime_counter_storages: OffsetPtr<*mut CounterStorage>,
    pub abs_runtime_counter_storages_base: *mut *mut CounterStorage,
    pub config_gen_ectx: OffsetPtr<ConfigGenEctx>,
    pub abs_config_gen_ectx: *mut ConfigGenEctx,
    pub packet_recirc_limit: u16,
    _pad0: [u8; 2],
    pub module_device_id: u32,
    pub device_target_count: u64,
    pub device_targets: OffsetPtr<ModuleDeviceTarget>,
    pub abs_device_targets: *mut ModuleDeviceTarget,
    pub object_link_count: u64,
    pub object_links: OffsetPtr<ModuleObjectLinkEctx>,
    pub abs_object_links: *mut ModuleObjectLinkEctx,
    pub module_prepared: OffsetPtr<u8>,
    pub abs_module_prepared: *mut u8,
}

/// Mirror of `struct rlist` (`common/rlist.h`) — raw worker-local links.
#[repr(C)]
pub struct Rlist {
    pub prev: *mut Rlist,
    pub next: *mut Rlist,
}

/// Worklist states of a device entry, for the owning worker only.
pub const DEVICE_ENTRY_SCHEDULE_READY: u8 = 0;
pub const DEVICE_ENTRY_SCHEDULE_HOME: u8 = 1;

/// Mirror of `struct device_entry_ectx` up to the fields the scheduling
/// path touches; the flexible pipeline map follows the fixed part.
#[repr(C)]
pub struct DeviceEntryEctx {
    pub handler: *mut c_void,
    pub schedule_node: Rlist,
    pub schedule_list: u8,
    pub direction: u8,
    _pad0: [u8; 6],
    pub device_ectx: OffsetPtr<DeviceEctx>,
    pub abs_device_ectx: *mut DeviceEctx,
    pub counter_packet_rx: *mut u64,
    pub counter_packet_entry: *mut u64,
    pub counter_packet_tx: *mut u64,
    pub counter_packet_drop: *mut u64,
    pub counter_packet_recirc_drop: *mut u64,
    pub counter_packet_pending_input: *mut u64,
    pub counter_packet_pending_output: *mut u64,
    pub pipeline_count: u64,
    pub pipeline_ptrs: OffsetPtr<*mut PipelineEctx>,
    pub pipelines: *mut *mut PipelineEctx,
    pub abs_pipelines: *mut *mut PipelineEctx,
    pub pipeline_map_size: u64,
    pub schedule: PacketFront,
}

/// Opaque `struct pipeline_ectx`.
#[repr(C)]
pub struct PipelineEctx {
    _private: [u8; 0],
}

/// Opaque `struct device_ectx`.
#[repr(C)]
pub struct DeviceEctx {
    _private: [u8; 0],
}

/// Mirror of `struct config_gen_ectx` up to the ready list the
/// scheduling path touches.
#[repr(C)]
pub struct ConfigGenEctx {
    pub cp_config_gen: OffsetPtr<CpConfigGen>,
    pub phy_device_maps: OffsetPtr<PhyDeviceMap>,
    pub counter_storage_registry: OffsetPtr<CounterStorageRegistry>,
    pub object_count: u64,
    pub objects: OffsetPtr<OffsetPtr<ObjectEctx>>,
    pub packet_front: PacketFront,
    pub entry_list: Rlist,
    pub ready_list: Rlist,
    pub schedules_ready: u8,
    _pad0: [u8; 7],
    pub device_ptrs: OffsetPtr<OffsetPtr<DeviceEctx>>,
    pub device_count: u64,
    // Trailing flexible array of absolute device addresses.
    pub devices: [*mut DeviceEctx; 0],
}

/// Opaque `struct cp_config_gen`.
#[repr(C)]
pub struct CpConfigGen {
    _private: [u8; 0],
}

/// Opaque `struct phy_device_map`.
#[repr(C)]
pub struct PhyDeviceMap {
    _private: [u8; 0],
}

/// Opaque `struct cp_config_counter_storage_registry`.
#[repr(C)]
pub struct CounterStorageRegistry {
    _private: [u8; 0],
}

// ---------------------------------------------------------------------------
// worker
// ---------------------------------------------------------------------------

/// Mirror of `struct tsc_clock` (`lib/dataplane/time/clock.h`).
#[repr(C)]
pub struct TscClock {
    pub tsc_to_ns_mult: u64,
    pub real_time_ns: u64,
    pub timestamp_counter: u64,
}

/// Mirror of `struct dp_worker` (`lib/dataplane/config/zone.h`).
#[repr(C)]
pub struct DpWorker {
    pub idx: u64,
    pub generation: u64,
    pub clock: TscClock,
    pub current_time: u64,
    pub iterations: *mut u64,
    pub rx_count: *mut u64,
    pub rx_size: *mut u64,
    pub tx_count: *mut u64,
    pub tx_size: *mut u64,
    pub remote_rx_count: *mut u64,
    pub remote_tx_count: *mut u64,
    pub local_tx_drops: *mut u64,
    pub remote_tx_drops: *mut u64,
    pub drop_count: *mut u64,
    pub rx_mempool: *mut c_void,
    pub rx_bursts: *mut u64,
    pub core_id: u32,
    pub device_id: u32,
    pub queue_id: u32,
    pub rx_burst_size: u32,
    pub config_gen_ectx: OffsetPtr<ConfigGenEctx>,
}

/// Opaque `struct dp_config` — commit handlers receive it but the module
/// contract keeps its contents the dataplane's own.
#[repr(C)]
pub struct DpConfig {
    _private: [u8; 0],
}

// ---------------------------------------------------------------------------
// pinned sizes and offsets, mirroring plugin_abi_assert.h
// ---------------------------------------------------------------------------

const _: () = assert!(core::mem::size_of::<Module>() == 112);
const _: () = assert!(core::mem::size_of::<Packet>() == 48);
const _: () = assert!(core::mem::size_of::<PacketList>() == 16);
const _: () = assert!(core::mem::size_of::<PacketFront>() == 128);
const _: () = assert!(core::mem::size_of::<RteMbuf>() == 128);
const _: () = assert!(core::mem::size_of::<ModuleEctx>() == 200);
const _: () = assert!(core::mem::size_of::<ModuleDeviceTarget>() == 40);
const _: () = assert!(core::mem::size_of::<ModuleObjectLinkEctx>() == 24);
const _: () = assert!(core::mem::size_of::<ObjectEctx>() == 24);
const _: () = assert!(core::mem::size_of::<DeviceEntryEctx>() == 272);
const _: () = assert!(core::mem::size_of::<ConfigGenEctx>() == 224);
const _: () = assert!(core::mem::size_of::<DpWorker>() == 168);
const _: () = assert!(core::mem::size_of::<TscClock>() == 24);
const _: () = assert!(core::mem::align_of::<RteMbuf>() == 64);

const _: () = assert!(core::mem::offset_of!(Packet, mbuf) == 8);
const _: () = assert!(core::mem::offset_of!(Packet, data_len) == 38);
const _: () = assert!(core::mem::offset_of!(PacketFront, input) == 0);
const _: () = assert!(core::mem::offset_of!(ModuleEctx, abs_cp_module) == 16);
const _: () = assert!(core::mem::offset_of!(ModuleEctx, abs_counter_storage) == 72);
const _: () = assert!(core::mem::offset_of!(ModuleEctx, packet_recirc_limit) == 128);
const _: () = assert!(core::mem::offset_of!(ModuleEctx, abs_device_targets) == 152);
const _: () = assert!(core::mem::offset_of!(ModuleEctx, abs_object_links) == 176);
const _: () = assert!(core::mem::offset_of!(ModuleEctx, abs_module_prepared) == 192);
const _: () = assert!(core::mem::offset_of!(DeviceEntryEctx, schedule) == 144);
const _: () = assert!(core::mem::offset_of!(ConfigGenEctx, ready_list) == 184);
const _: () = assert!(core::mem::offset_of!(DpWorker, current_time) == 40);
