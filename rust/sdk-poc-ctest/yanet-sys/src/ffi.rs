// Hand-written mirrors of the C library layouts the SDK touches; no module
// is named here.
//
// This file is the ctest input: the systest crate expands it as a standalone
// crate, with the `ctest` cfg set so that the zerocopy derives drop out, and
// checks every public struct, union, field, relative-pointer alias and
// constant against the C headers. It must therefore stay self-contained (only
// `core` paths, no `crate::` references, no inner attributes) and keep the C
// spelling of every name, so ctest maps `packet` to `struct packet` without a
// rename table.

use core::{
    cell::UnsafeCell,
    ffi::{c_char, c_int, c_void},
    marker::PhantomData,
    mem::{ManuallyDrop, MaybeUninit},
};

/// Self-relative pointer as written by the C `SET_OFFSET_OF` macro.
///
/// The stored value is the signed distance from the slot's own address to the
/// target; zero means NULL. Any bit pattern is a valid value, which is what
/// makes it `FromBytes`: an offset is inert data, and only the validated
/// configuration graph plus the resolver turn it into an access. It is
/// neither `Copy` nor `Clone`, because a value moved out of its slot would
/// silently point elsewhere.
#[repr(transparent)]
#[cfg_attr(not(ctest), derive(zerocopy::FromBytes, zerocopy::KnownLayout))]
pub struct RelPtr<T> {
    offset: isize,
    _target: PhantomData<*const T>,
}

/// Bytes owned and mutated by C after publication, never read from Rust.
///
/// The interior-mutable wrapper keeps a shared reference to a structure that
/// embeds it from asserting that these bytes stay unchanged, so C may
/// rewrite them while Rust holds such a reference. Rust never reads them,
/// so no data race arises. Any bit pattern is acceptable.
#[repr(transparent)]
#[cfg_attr(not(ctest), derive(zerocopy::FromBytes))]
pub struct Opaque<T>(UnsafeCell<MaybeUninit<T>>);

impl<T> RelPtr<T> {
    /// Raw stored offset; inert data that grants no access by itself.
    pub fn offset(&self) -> isize {
        self.offset
    }
}

// Monomorphic shadows of generic relative pointers.
//
// ctest cannot translate a generic path such as `RelPtr<lpm_page>` into a C
// type, so every relative-pointer field names one of these aliases instead
// and the systest build maps each alias to the C pointer type it shadows.
// Size, alignment, offset and the C field type are then all checked.

/// `struct lpm_page **` in `struct lpm`: offset to an array of chunk offsets.
pub type rel_lpm_chunks = RelPtr<RelPtr<lpm_page>>;
/// `struct lpm_page *` element of the chunk array.
pub type rel_lpm_chunk = RelPtr<lpm_page>;
/// `struct lpm_page *` member of `union lpm_value`.
pub type rel_lpm_value_page = ManuallyDrop<RelPtr<lpm_page>>;
/// `struct memory_context` embedded in `struct lpm`, mutated by C.
pub type opaque_memory_context = Opaque<memory_context>;

// Opaque C types: referenced only through pointers, never dereferenced here.

pub enum agent {}
pub enum block_allocator {}
pub enum config_gen_ectx {}
pub enum counter {}
pub enum counter_storage {}
pub enum counter_value_handle {}
pub enum cp_module_counter_registry {}
pub enum cp_module_device {}
pub enum cp_module_object {}
pub enum device_entry_ectx {}
pub enum dp_config {}
pub enum dp_worker {}
pub enum module_object_link_ectx {}
pub enum rte_mbuf {}

pub const YANET_MODULE_ABI_VERSION: u32 = 33;
pub const MODULE_TYPE_LEN: usize = 80;
pub const CP_MODULE_NAME_LEN: usize = 80;
pub const COUNTER_POOL_SIZE: usize = 7;
pub const MEMORY_CONTEXT_NAME_SIZE: usize = 64;

pub const LPM_VALUE_INVALID: u32 = 0xffff_ffff;
pub const LPM_VALUE_FLAG: u64 = 0x0000_0001;
pub const LPM_CHUNK_SIZE: usize = 16;

pub const PACKET_FLAG_FRAGMENTED: u32 = 0;
pub const RTE_ETHER_TYPE_IPV4: u16 = 0x0800;
pub const RTE_ETHER_TYPE_IPV6: u16 = 0x86dd;
pub const IPPROTO_FRAGMENT: u8 = 44;

// Byte offsets into the DPDK `struct rte_mbuf`.
//
// The mbuf itself is too large and too union-heavy to mirror; only the three
// fields the packet view reads are located, and the systest header computes
// the same constants with `offsetof`/`sizeof` so ctest checks them.

pub const YANET_RS_MBUF_BUF_ADDR_OFFSET: usize = 0;
pub const YANET_RS_MBUF_DATA_OFF_OFFSET: usize = 16;
pub const YANET_RS_MBUF_DATA_OFF_SIZE: usize = 2;
pub const YANET_RS_MBUF_DATA_LEN_OFFSET: usize = 40;
pub const YANET_RS_MBUF_DATA_LEN_SIZE: usize = 2;

#[repr(C)]
pub struct registry_item {
    pub refcnt: u64,
    pub destroying: u64,
}

#[repr(C)]
pub struct str_index {
    pub memory_context: *mut memory_context,
    pub values: *mut u32,
    pub size: u32,
    pub capacity: u32,
}

#[repr(C)]
pub struct counter_registry {
    pub memory_context: *mut memory_context,
    pub r#gen: u64,
    pub capacity: u64,
    pub count: u64,
    pub counts: [u64; COUNTER_POOL_SIZE],
    pub names: *mut counter,
    pub str_index: str_index,
}

/// Allocation context embedded in C structures.
///
/// Its tree links are rewritten by C after publication when a sibling context
/// is finalised, so no Rust reference may ever cover these bytes.
#[repr(C)]
pub struct memory_context {
    pub block_allocator: *mut block_allocator,
    pub balloc_count: usize,
    pub bfree_count: usize,
    pub balloc_size: usize,
    pub bfree_size: usize,
    pub name: [c_char; MEMORY_CONTEXT_NAME_SIZE],
    pub parent: *mut memory_context,
    pub first_child: *mut memory_context,
    pub next_sibling: *mut memory_context,
}

/// Module configuration header; C mutates its registry item under the
/// control-plane lock after publication, so it is only ever projected.
#[repr(C)]
pub struct cp_module {
    pub config_item: registry_item,
    pub dp_module_idx: u64,
    pub r#type: [c_char; 80],
    pub name: [c_char; CP_MODULE_NAME_LEN],
    pub r#gen: u64,
    pub counter_registry: counter_registry,
    pub runtime_counter_registry_count: u64,
    pub runtime_counter_registries: *mut *mut cp_module_counter_registry,
    pub rx_counter_id: u64,
    pub tx_counter_id: u64,
    pub drop_counter_id: u64,
    pub pending_input_counter_id: u64,
    pub pending_output_counter_id: u64,
    pub prev: *mut cp_module,
    pub agent: *mut agent,
    pub memory_context: memory_context,
    pub device_count: u64,
    pub devices: *mut cp_module_device,
    pub object_count: u64,
    pub objects: *mut cp_module_object,
}

/// One LPM slot: a leaf value when the low bit is set, otherwise the offset of
/// the child page relative to the slot itself.
#[repr(C)]
#[cfg_attr(not(ctest), derive(zerocopy::FromBytes, zerocopy::KnownLayout))]
pub union lpm_value {
    pub page: rel_lpm_value_page,
    pub value: u64,
}

#[repr(C)]
#[cfg_attr(not(ctest), derive(zerocopy::FromBytes, zerocopy::KnownLayout))]
pub struct lpm_page {
    pub values: [lpm_value; 256],
}

/// An LPM tree as embedded in a module configuration.
///
/// The memory context is opaque because C rewrites its sibling links after
/// publication; a reference to the whole tree is therefore sound.
#[repr(C)]
#[cfg_attr(not(ctest), derive(zerocopy::FromBytes, zerocopy::KnownLayout))]
pub struct lpm {
    pub memory_context: opaque_memory_context,
    pub pages: rel_lpm_chunks,
    pub page_count: usize,
}

// Function-pointer fields are spelled inline rather than through aliases so
// that the ctest field-type check compares the full C signature.

/// Module descriptor returned by `new_module_<name>`; the loader copies the
/// fields and releases the block with `free`.
#[repr(C)]
pub struct module {
    pub name: [c_char; MODULE_TYPE_LEN],
    pub handler: Option<
        unsafe extern "C" fn(dp_worker: *mut dp_worker, module_ectx: *mut module_ectx, front: *mut packet_front),
    >,
    pub commit_handler: Option<unsafe extern "C" fn(dp_config: *mut dp_config, cp_module: *mut cp_module)>,
    pub commit_ectx_handler: Option<unsafe extern "C" fn(module_ectx: *mut module_ectx, cp_module: *mut cp_module)>,
    pub prepared_size: u64,
}

#[repr(C)]
pub struct module_device_target {
    pub device_id: u16,
    pub input_entry: *mut device_entry_ectx,
    pub output_entry: *mut device_entry_ectx,
    pub abs_input_entry: *mut device_entry_ectx,
    pub abs_output_entry: *mut device_entry_ectx,
}

#[repr(C)]
pub struct module_ectx {
    pub handler: Option<
        unsafe extern "C" fn(dp_worker: *mut dp_worker, module_ectx: *mut module_ectx, front: *mut packet_front),
    >,
    pub cp_module: *mut cp_module,
    pub abs_cp_module: *mut cp_module,
    pub rx_counter: *mut counter_value_handle,
    pub tx_counter: *mut counter_value_handle,
    pub drop_counter: *mut counter_value_handle,
    pub pending_input_counter: *mut counter_value_handle,
    pub pending_output_counter: *mut counter_value_handle,
    pub counter_storage: *mut counter_storage,
    pub abs_counter_storage: *mut counter_storage,
    pub runtime_counter_storage_count: u64,
    pub runtime_counter_storages: *mut *mut counter_storage,
    pub abs_runtime_counter_storages: *mut *mut counter_storage,
    pub abs_runtime_counter_storages_base: *mut *mut counter_storage,
    pub config_gen_ectx: *mut config_gen_ectx,
    pub abs_config_gen_ectx: *mut config_gen_ectx,
    pub packet_recirc_limit: u16,
    pub module_device_id: u32,
    pub device_target_count: u64,
    pub device_targets: *mut module_device_target,
    pub abs_device_targets: *mut module_device_target,
    pub object_link_count: u64,
    pub object_links: *mut module_object_link_ectx,
    pub abs_object_links: *mut module_object_link_ectx,
    pub module_prepared: *mut c_void,
    pub abs_module_prepared: *mut c_void,
}

#[repr(C)]
pub struct network_header {
    pub r#type: u16,
    pub offset: u16,
}

#[repr(C)]
pub struct transport_header {
    pub r#type: u16,
    pub offset: u16,
}

#[repr(C)]
pub struct packet {
    pub next: *mut packet,
    pub mbuf: *mut rte_mbuf,
    pub hash: u32,
    pub rx_device_id: u16,
    pub tx_device_id: u16,
    pub flags: u16,
    pub vlan: u16,
    pub recirc_remaining: u16,
    pub recirc_initialized: u8,
    pub flow_label: u32,
    pub fragment_offset: u16,
    pub data_len: u16,
    pub network_header: network_header,
    pub transport_header: transport_header,
}

#[repr(C)]
pub struct packet_list {
    pub first: *mut packet,
    pub last: *mut *mut packet,
}

#[repr(C)]
pub struct packet_front {
    pub input: packet_list,
    pub output: packet_list,
    pub drop: packet_list,
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

/// C signature of the dataplane's tunnel stripper, exported by the dataplane
/// binary and resolved when the plugin is loaded.
pub type packet_decap_fn = unsafe extern "C" fn(packet: *mut packet) -> c_int;
