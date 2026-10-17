//! Counter registry and per-worker storage mirrors.

use core::ffi::c_char;

use crate::{memory::MemoryContext, offset::OffsetPtr};

/// Counter name capacity, counting the terminating zero.
pub const COUNTER_NAME_LEN: usize = 128;

/// Registry id that names no counter.
pub const COUNTER_INVALID: u64 = u64::MAX;

/// Size classes pooled by a storage.
pub const COUNTER_POOL_SIZE: usize = 7;

/// Mirror of `struct str_index` (`common/str_index.h`), embedded by value
/// in every registry.
#[repr(C)]
#[derive(Default)]
pub struct StrIndex {
    pub memory_context: OffsetPtr<MemoryContext>,
    pub values: OffsetPtr<u32>,
    pub size: u32,
    pub capacity: u32,
}

/// Mirror of `struct counter` — one registered counter's schema row.
#[repr(C)]
pub struct CounterSchema {
    pub name: [c_char; COUNTER_NAME_LEN],
    pub size: u64,
    pub generation: u64,
    pub offset: u64,
}

/// Mirror of `struct counter_registry` (`lib/counters/counters.h`).
///
/// The schema lives on the control-plane side; the dataplane only needs
/// the layout so the embedded registry sits at the right offset inside
/// `struct cp_module`.
#[repr(C)]
pub struct CounterRegistry {
    pub memory_context: OffsetPtr<MemoryContext>,
    pub generation: u64,
    pub capacity: u64,
    pub count: u64,
    pub counts: [u64; COUNTER_POOL_SIZE],
    pub names: OffsetPtr<CounterSchema>,
    pub str_index: StrIndex,
}

impl Default for CounterRegistry {
    fn default() -> Self {
        // SAFETY: all-zero is the valid empty registry (null offsets,
        // zero counts), exactly what cp_module_init starts from.
        unsafe { core::mem::zeroed() }
    }
}

/// The values array a handle resolves to; opaque because its length is
/// the registered counter size.
#[repr(C)]
pub struct CounterValueHandle {
    _private: [u8; 0],
}

#[repr(C)]
struct CounterStorageBlock {
    refcnt: u64,
    pages: OffsetPtr<CounterStoragePage>,
}

#[repr(C)]
struct CounterStoragePage {
    values: [u64; COUNTER_STORAGE_PAGE_SLOTS],
}

/// Values per pooled page.
const COUNTER_STORAGE_PAGE_SLOTS: usize = 4096 / core::mem::size_of::<u64>();

#[repr(C)]
struct CounterStoragePool {
    block_count: u64,
    blocks: OffsetPtr<OffsetPtr<CounterStorageBlock>>,
}

/// Mirror of `struct counter_storage` (`lib/counters/counters.h`).
///
/// One per module per worker: values are single-writer, so the dataplane
/// bumps them with plain loads and stores.
#[repr(C)]
pub struct CounterStorage {
    pub memory_context: OffsetPtr<MemoryContext>,
    pub counter_value_handles: OffsetPtr<OffsetPtr<CounterValueHandle>>,
    pub registry: OffsetPtr<CounterRegistry>,
    pools: [CounterStoragePool; COUNTER_POOL_SIZE],
    pub refcnt: u64,
}

impl Default for CounterStorage {
    fn default() -> Self {
        // SAFETY: all-zero is the valid empty storage (null offsets,
        // empty pools).
        unsafe { core::mem::zeroed() }
    }
}

/// Resolve a counter's values array, mirroring `counter_get_address`.
///
/// The handle is the values array itself; size-2 counters lay out as
/// `[packets, bytes]`.
pub fn counter_get_address(storage: &CounterStorage, counter_id: u64) -> *mut u64 {
    // SAFETY: the handle array is filled at storage spawn time and never
    // changes while the storage lives; counter_id comes from the module's
    // own registry, which spawn expanded in full.
    unsafe {
        let handles = storage.counter_value_handles.resolve_non_null();
        let handle_field = &*handles.add(counter_id as usize);
        handle_field.resolve_non_null() as *mut u64
    }
}

const _: () = assert!(core::mem::size_of::<StrIndex>() == 24);
const _: () = assert!(core::mem::size_of::<CounterRegistry>() == 120);
const _: () = assert!(core::mem::size_of::<CounterStorage>() == 144);

#[cfg(test)]
mod tests {
    use std::prelude::v1::*;

    use super::*;

    #[test]
    fn handle_resolves_to_values_array() {
        let mut values: Box<[u64; 2]> = Box::new([3, 100]);
        // OffsetPtr is self-relative (resolve is field-address plus stored
        // offset), so the link must be stored at its final slot: a stack
        // temporary's offset would dangle once copied.
        let mut handles: Box<[OffsetPtr<CounterValueHandle>; 1]> = Box::new([OffsetPtr::null()]);
        handles[0].store(values.as_mut_ptr() as *mut CounterValueHandle);

        let mut storage = CounterStorage::default();
        storage.counter_value_handles.store(handles.as_mut_ptr());

        let address = counter_get_address(&storage, 0);
        // SAFETY: aimed at a live [u64; 2] above.
        assert_eq!(unsafe { *address }, 3);
        assert_eq!(unsafe { *address.add(1) }, 100);
    }
}
