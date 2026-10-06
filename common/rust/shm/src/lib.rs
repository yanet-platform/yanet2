//! Shared-memory structure mirrors for YANET dataplane modules.
//!
//! These types reproduce, byte for byte, the layouts the control-plane C
//! libraries publish into shared memory: the offset-pointer protocol of
//! `common/memory_address.h`, the lookup structures of `common/` (`lpm`,
//! `value_table`, `vline`), the classifier halves of `lib/classify` and
//! `lib/filter`, and the `struct cp_module` prefix every module
//! configuration embeds first.
//!
//! The mirrors are read-side only: building and publishing configs stays
//! in the C api libraries. Compile-time size and offset asserts pin the
//! layouts against `lib/dataplane/config/plugin_abi_assert.h`, and the
//! dataplane_ut suite cross-checks them against the C headers in the
//! same binary.
//!
//! Reading a structure outside a live mapping, or past the end of what
//! its counts declare, is undefined behavior exactly as in C; the safe
//! wrappers in `yanet-dp` are the intended way to reach these types.
#![no_std]

extern crate alloc;

mod classify;
mod counter;
mod cp_module;
mod filter;
mod lpm;
mod memory;
mod offset;
mod value;

pub use classify::{
    ClassifyAttrNet4, ClassifyAttrNet6, FILTER_NET6_ROW_MARK, NET6_LEN, Net6Classifier, PROTO_UNAVAILABLE_ICMP,
    PROTO_UNAVAILABLE_ICMPV6, PROTO_UNAVAILABLE_TCP, ProtoRangeClassifier, classify_net4_lookup, classify_net6_lookup,
};
pub use counter::{
    COUNTER_INVALID, CounterRegistry, CounterSchema, CounterStorage, CounterValueHandle, counter_get_address,
};
pub use cp_module::{
    CP_MODULE_NAME_LEN, CpModule, CpModuleDevice, CpModuleObject, CpObject, ModuleConfig, ObjectConfig, RegistryItem,
};
pub use filter::{FILTER_RULE_INVALID, Filter, FilterSlots, FilterVertex, MAX_ATTRIBUTES, filter_query};
pub use lpm::{
    LPM_KEY_SIZE_MAX, LPM_VALUE_INVALID, Lpm, LpmPage, LpmValue, lpm_lookup, lpm_lookup_batch, lpm_value_get,
    lpm_value_set, lpm4_lookup, lpm8_lookup, lpm16_lookup,
};
pub use memory::{BlockAllocatorOpaque, MEMORY_CONTEXT_NAME_SIZE, MemoryContext};
pub use offset::OffsetPtr;
pub use value::{ValueTable, Vline, value_table_get, vline_get};

#[cfg(test)]
extern crate std;
