//! The address of a counter value in a counter storage.
//!
//! A counter storage holds one value per registered counter for one worker.
//! The dataplane reaches a value through the storage's handle table, as the
//! C `counter_get_address` does.

use yanet_shm::root::ShmRoot;

use crate::bindings;

/// The counter id C reports a failure with, its `(uint64_t)-1` cast macro that
/// bindgen does not bind.
pub const COUNTER_INVALID: u64 = u64::MAX;

/// Address of a counter's value in a counter storage, bit for bit like
/// the C `counter_get_address` (`lib/counters/counters.h`) without its
/// `COUNTERS_CHECK` null test.
///
/// The handle table holds one self-relative pointer per registered counter,
/// each naming the counter's first value, which is the handle C casts to
/// `uint64_t *`.
///
/// # Safety
///
/// The root must cover the mapping the storage lies in, the storage must be a
/// live counter storage whose handle table the control plane does not change
/// while the result is used, and the id an index of the registry the storage
/// was spawned from.
#[inline(always)]
pub(crate) unsafe fn counter_get_address(root: ShmRoot, storage: *mut bindings::counter_storage, id: u64) -> *mut u64 {
    // SAFETY: the storage is live and frozen (caller contract); the handle
    // table slot is a field of it, and the table it names holds one
    // self-relative offset per registered counter. Offsets are read as
    // integers, never as pointers.
    unsafe {
        let table_slot = &raw const (*storage).counter_value_handles;
        let table = root.rel::<isize>(table_slot.addr(), table_slot.cast::<isize>().read());
        let handle = table.add(id as usize);
        root.rel::<u64>(handle.addr(), handle.read())
    }
}

#[cfg(test)]
mod tests {
    use core::mem::{offset_of, size_of};

    use yanet_sdk_testing::BlockArena;
    use yanet_shm::rel::RelPtr;

    use super::counter_get_address;
    use crate::bindings;

    /// Points the relative slot at `slot` to `target`.
    fn point(arena: &'static BlockArena, slot: usize, target: usize) {
        // SAFETY: the slot is a word of a live block of the arena; nothing
        // references it.
        unsafe {
            arena
                .root()
                .ptr::<isize>(slot)
                .write(target.wrapping_sub(slot) as isize)
        }
    }

    /// verifies that the address of a counter is the value its handle names,
    /// for every counter of the storage, and that a write through it reaches
    /// that value only.
    ///
    /// The counter storage is laid out by hand as the C storage spawn lays it
    /// out: a handle table of self-relative pointers, each to a counter's
    /// value.
    #[test]
    fn test_counter_get_address_follows_the_handle_table() {
        let arena = BlockArena::new();
        let storage = arena.alloc(size_of::<bindings::counter_storage>()).as_ptr().addr();
        let table = arena.alloc(3 * size_of::<RelPtr<u64>>()).as_ptr().addr();
        let values = [0, 1, 2].map(|_| arena.alloc(2 * size_of::<u64>()).as_ptr().addr());
        point(
            arena,
            storage + offset_of!(bindings::counter_storage, counter_value_handles),
            table,
        );
        // Handles in an order of their own, so an id is not just an address.
        for (idx, value) in [2, 0, 1].into_iter().enumerate() {
            point(arena, table + idx * size_of::<RelPtr<u64>>(), values[value]);
        }
        let root = arena.root();
        let storage = root.ptr::<bindings::counter_storage>(storage);

        for id in 0..3 {
            // SAFETY: the storage is laid out as C lays it out and frozen for
            // the test; the id indexes its three handles.
            let value = unsafe { counter_get_address(root, storage, id) };
            // SAFETY: the address names the first word of a live value block.
            unsafe { value.write(10 + id) };
        }

        assert_eq!(
            [11, 12, 10],
            values.map(|value| {
                // SAFETY: a live value block, written above.
                unsafe { root.ptr::<u64>(value).read() }
            })
        );
    }
}
