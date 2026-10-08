//! ABI size and offset getters for the C-side cross-check test.
//!
//! The dataplane_ut suite links these next to the real headers and
//! fails the build-time test on any divergence, on top of the const
//! asserts in [`crate::raw`].

use crate::raw;

macro_rules! abi_size {
    ($(#[$meta:meta])* $name:ident, $ty:ty) => {
        $(#[$meta])*
        #[unsafe(no_mangle)]
        pub extern "C" fn $name() -> usize {
            core::mem::size_of::<$ty>()
        }
    };
}

macro_rules! abi_offset {
    ($(#[$meta:meta])* $name:ident, $ty:ty, $field:ident) => {
        $(#[$meta])*
        #[unsafe(no_mangle)]
        pub extern "C" fn $name() -> usize {
            core::mem::offset_of!($ty, $field)
        }
    };
}

abi_size!(
    /// Size of `struct module` as compiled in Rust.
    yanet_dp_sizeof_module,
    raw::Module
);
abi_size!(yanet_dp_sizeof_packet, raw::Packet);
abi_size!(yanet_dp_sizeof_packet_front, raw::PacketFront);
abi_size!(yanet_dp_sizeof_module_ectx, raw::ModuleEctx);
abi_size!(yanet_dp_sizeof_dp_worker, raw::DpWorker);
abi_size!(yanet_dp_sizeof_rte_mbuf, raw::RteMbuf);
abi_size!(yanet_dp_sizeof_cp_module, yanet_shm::CpModule);
abi_size!(yanet_dp_sizeof_memory_context, yanet_shm::MemoryContext);
abi_size!(yanet_dp_sizeof_lpm, yanet_shm::Lpm);
abi_size!(yanet_dp_sizeof_value_table, yanet_shm::ValueTable);
abi_size!(yanet_dp_sizeof_vline, yanet_shm::Vline);
abi_size!(yanet_dp_sizeof_filter, yanet_shm::Filter);

abi_offset!(yanet_dp_offset_packet_mbuf, raw::Packet, mbuf);
abi_offset!(yanet_dp_offset_packet_data_len, raw::Packet, data_len);
abi_offset!(
    /// Offset of `next` inside `struct rte_mbuf`; pins the standard
    /// IOVA-in-mbuf cacheline shape against the DPDK build config.
    yanet_dp_offset_rte_mbuf_next,
    raw::RteMbuf,
    next
);
abi_offset!(yanet_dp_offset_rte_mbuf_buf_addr, raw::RteMbuf, buf_addr);
abi_offset!(yanet_dp_offset_rte_mbuf_data_off, raw::RteMbuf, data_off);
abi_offset!(yanet_dp_offset_rte_mbuf_pkt_len, raw::RteMbuf, pkt_len);
abi_offset!(yanet_dp_offset_rte_mbuf_data_len, raw::RteMbuf, data_len);
abi_offset!(
    yanet_dp_offset_module_ectx_abs_cp_module,
    raw::ModuleEctx,
    abs_cp_module
);
abi_offset!(
    yanet_dp_offset_module_ectx_abs_counter_storage,
    raw::ModuleEctx,
    abs_counter_storage
);
abi_offset!(
    yanet_dp_offset_module_ectx_packet_recirc_limit,
    raw::ModuleEctx,
    packet_recirc_limit
);
abi_offset!(
    yanet_dp_offset_module_ectx_abs_module_prepared,
    raw::ModuleEctx,
    abs_module_prepared
);
abi_offset!(
    yanet_dp_offset_device_entry_ectx_schedule,
    raw::DeviceEntryEctx,
    schedule
);
abi_offset!(
    yanet_dp_offset_config_gen_ectx_ready_list,
    raw::ConfigGenEctx,
    ready_list
);
abi_offset!(yanet_dp_offset_dp_worker_current_time, raw::DpWorker, current_time);

/// The ABI version this SDK compiled against.
///
/// Cross-checked against the real header by the dataplane_ut C test, so
/// the mirror cannot silently drift when the header bumps (as it did
/// for 33).
#[unsafe(no_mangle)]
pub extern "C" fn yanet_dp_abi_version() -> u32 {
    raw::YANET_MODULE_ABI_VERSION
}

/// The ABI version a plugin build exports; the loader checks it against
/// the dataplane binary for `.so` modules.
///
/// Feature-gated: only a plugin build (`--features yanet-dp/plugin`) may
/// define the symbol. The statically linked built-ins never may — two
/// Rust modules linked into the same dataplane binary would collide on
/// it, which is why the C side keeps its copy out of the static module
/// libraries too (lib/dataplane/config/meson.build).
#[cfg(feature = "plugin")]
#[unsafe(no_mangle)]
pub static yanet_module_abi_version: u32 = raw::YANET_MODULE_ABI_VERSION;
