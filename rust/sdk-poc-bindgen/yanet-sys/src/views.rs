//! Declarations of every C aggregate the SDK projects into.
//!
//! Each pointer-carrying field of a declared aggregate must be classified;
//! the build fails otherwise. Classifications follow the comments of the C
//! headers: a field documented as an offset pointer is `rel`, a field the
//! publishing process absolutizes for the hot path is `abs`, pointers into
//! memory outside the shared mapping (packets, mbufs) are `ffi`.

use crate::{
    bindings,
    rel::{RelPtr, RelRef},
    shm::Opaque,
};

/// Bytes Rust never reads, used as the target of classified but unused
/// pointer fields.
#[repr(C)]
pub struct COpaque {
    _bytes: [u8; 0],
    _pinned: core::marker::PhantomPinned,
}

/// Pages of one LPM chunk, allocated as a single block.
pub type LpmChunk = [LpmPage; bindings::LPM_CHUNK_SIZE as usize];

crate::layout::layout! {
    /// Mirror of the C LPM header.
    ///
    /// The embedded memory context is opaque: its sibling link may be
    /// rewritten after publish when another context under the same parent
    /// is finalised.
    mirror LpmRaw = lpm {
        opaque memory_context: Opaque<bindings::memory_context>,
        rel pages: RelPtr<RelRef<LpmChunk>>,
        plain page_count: usize,
    }

    /// One LPM trie page: 256 slots indexed by a key byte.
    mirror LpmPage = lpm_page {
        embed values: [LpmValue; 256],
    }

    /// LPM slot: a leaf value with the low flag bit set, or a relative
    /// pointer to the child page.
    mirror_union LpmValue = lpm_value {
        rel page: RelPtr<LpmPage>,
        plain value: u64,
    }

    /// Module execution context, frozen once handed to the worker.
    frozen ModuleEctx = module_ectx {
        opaque handler,
        rel cp_module: RelPtr<COpaque>,
        abs abs_cp_module: COpaque,
        abs rx_counter: COpaque,
        abs tx_counter: COpaque,
        abs drop_counter: COpaque,
        abs pending_input_counter: COpaque,
        abs pending_output_counter: COpaque,
        rel counter_storage: RelPtr<COpaque>,
        abs abs_counter_storage: COpaque,
        rel runtime_counter_storages: RelPtr<COpaque>,
        rel abs_runtime_counter_storages: RelPtr<COpaque>,
        abs abs_runtime_counter_storages_base: COpaque,
        rel config_gen_ectx: RelPtr<COpaque>,
        abs abs_config_gen_ectx: COpaque,
        rel device_targets: RelPtr<COpaque>,
        abs abs_device_targets: COpaque,
        rel object_links: RelPtr<COpaque>,
        abs abs_object_links: COpaque,
        rel module_prepared: RelPtr<COpaque>,
        abs abs_module_prepared: COpaque,
    }

    /// Packet descriptor, owned by the worker for the round.
    raw PacketRaw = packet {
        ffi next: bindings::packet,
        ffi mbuf: bindings::rte_mbuf,
        plain flags: u16,
        plain flow_label: u32,
        plain data_len: u16,
        plain network_header: bindings::network_header,
        plain transport_header: bindings::transport_header,
    }

    /// Intrusive packet list.
    raw PacketListRaw = packet_list {
        ffi first: bindings::packet,
        ffi last: *mut bindings::packet,
    }

    /// Input, output and drop lists of one pipeline stage with counters.
    raw PacketFrontRaw = packet_front {
        embed input: bindings::packet_list,
        embed output: bindings::packet_list,
        embed drop: bindings::packet_list,
        plain output_count: u64,
        plain output_bytes: u64,
        plain drop_count: u64,
        plain drop_bytes: u64,
    }

    /// DPDK packet buffer: only the first-segment geometry is read.
    raw MbufRaw = rte_mbuf {
        ffi buf_addr: core::ffi::c_void,
        plain data_off: u16,
        plain data_len: u16,
        opaque pool,
        opaque next,
        opaque shinfo,
    }
}
