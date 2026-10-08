//! Signature-tree filter mirror (`lib/filter`).

use alloc::vec::Vec;

use crate::{
    memory::MemoryContext,
    value::{ValueTable, value_table_get},
};

/// Upper bound on attributes per filter signature.
pub const MAX_ATTRIBUTES: usize = 10;

/// Result of a query that matched no rule.
pub const FILTER_RULE_INVALID: u32 = 0xffff_ffff;

/// Mirror of `struct value_collector` — compile-time scratch embedded in
/// every registry; the dataplane never touches it.
#[repr(C)]
pub struct ValueCollector {
    pub memory_context: crate::offset::OffsetPtr<MemoryContext>,
    pub use_map: crate::offset::OffsetPtr<crate::offset::OffsetPtr<u32>>,
    pub chunk_count: u32,
    pub generation: u32,
}

/// Mirror of `struct value_range`.
#[repr(C)]
pub struct ValueRange {
    pub values: crate::offset::OffsetPtr<u32>,
    pub count: u64,
}

/// Mirror of `struct value_registry` (`common/registry.h`).
#[repr(C)]
pub struct ValueRegistry {
    pub memory_context: crate::offset::OffsetPtr<MemoryContext>,
    pub collector: ValueCollector,
    pub ranges: crate::offset::OffsetPtr<ValueRange>,
    pub range_count: u64,
    pub max_value: u32,
}

/// Mirror of `struct filter_vertex` (`lib/filter/filter.h`).
///
/// A leaf carries the compiled attribute payload in `data`; an inner
/// vertex joins its two children through `table`.
#[repr(C)]
pub struct FilterVertex {
    pub registry: ValueRegistry,
    pub table: ValueTable,
    pub data: crate::offset::OffsetPtr<u8>,
}

/// Mirror of `struct filter` (`lib/filter/filter.h`).
///
/// `v` is the array-based binary tree over a fixed attribute signature:
/// leaves at `lookup_count..lookup_count + n`, inner vertices between,
/// and the whole tree is immutable once published.
#[repr(C)]
pub struct Filter {
    pub v: [FilterVertex; 2 * MAX_ATTRIBUTES],
    pub memory_context: MemoryContext,
}

impl Default for Filter {
    fn default() -> Self {
        // SAFETY: all-zero is the valid unbuilt state of every member.
        unsafe { core::mem::zeroed() }
    }
}

const _: () = assert!(core::mem::size_of::<ValueCollector>() == 24);
const _: () = assert!(core::mem::size_of::<ValueRegistry>() == 56);
const _: () = assert!(core::mem::size_of::<FilterVertex>() == 88);
const _: () = assert!(core::mem::size_of::<Filter>() == 1888);

/// Slot storage one query needs: `2 * MAX_ATTRIBUTES` per packet plus a
/// constant zero for a single-attribute signature.
#[derive(Debug)]
pub struct FilterSlots {
    slots: Vec<u32>,
}

impl FilterSlots {
    /// Slots for batches of up to `capacity` packets.
    pub fn with_capacity(capacity: usize) -> Self {
        Self {
            slots: alloc::vec![0; 2 * MAX_ATTRIBUTES * capacity + 1],
        }
    }

    /// The raw slot buffer, sized for `count` packets.
    pub fn for_count(&mut self, count: usize) -> &mut [u32] {
        let needed = 2 * MAX_ATTRIBUTES * count + 1;
        if self.slots.len() < needed {
            self.slots.resize(needed, 0);
        }
        &mut self.slots[..needed]
    }
}

/// Run one signature-tree query over a batch, mirroring `filter_query`.
///
/// `leaf_count` is the signature's attribute count; `leaf` evaluates one
/// leaf for every packet at once, receiving the leaf's compiled payload
/// (the vertex `data` payload) and a `count`-long output slice it must
/// fully overwrite. `results` receives one rule id per packet, or
/// [`FILTER_RULE_INVALID`] where no rule matched.
///
/// `slots` is scratch, borrowed per call; [`FilterSlots`] sizes it.
pub fn filter_query<E>(
    filter: &Filter,
    leaf_count: usize,
    leaf: E,
    count: usize,
    slots: &mut [u32],
    results: &mut [u32],
) where
    E: Fn(usize /* leaf */, *mut u8, &mut [u32]),
{
    debug_assert!(leaf_count >= 1);
    debug_assert!(leaf_count <= MAX_ATTRIBUTES);
    debug_assert_eq!(results.len(), count);
    debug_assert!(slots.len() > 2 * MAX_ATTRIBUTES * count);

    // Compute classifiers for the leaf attributes into their slots.
    for leaf_idx in 0..leaf_count {
        let vertex = leaf_count + leaf_idx;
        // A published signature tree keeps the payload of its declared
        // leaves; the leaf evaluator mirrors the C attribute query for
        // the vertex's attribute kind.
        let data = filter.v[vertex].data.resolve_non_null();
        let out_start = vertex * count;
        leaf(leaf_idx, data, &mut slots[out_start..out_start + count]);
    }

    // Fold inner vertices (except the root) up into their parents.
    for vertex in (2..leaf_count).rev() {
        for idx in 0..count {
            let left = slots[(vertex << 1) * count + idx];
            let right = slots[(vertex << 1 | 1) * count + idx];
            slots[vertex * count + idx] = value_table_get(&filter.v[vertex].table, left, right);
        }
    }

    // The root is vertex 1 for multi-attribute signatures, else 0.
    let root = usize::from(leaf_count > 1);
    for idx in 0..count {
        let left = if root == 0 { 0 } else { slots[(root << 1) * count + idx] };
        let right = slots[(root << 1 | 1) * count + idx];
        results[idx] = value_table_get(&filter.v[root].table, left, right);
    }
}

#[cfg(test)]
mod tests {
    use std::prelude::v1::*;

    use super::*;
    use crate::offset::OffsetPtr;

    /// A one-attribute filter whose single leaf reads `data[leaf_idx]`
    /// and whose root row maps class 5 to rule 9.
    fn fixture() -> (Box<Filter>, Box<[u8; 1]>) {
        let mut filter = Box::new(Filter::default());
        let mut payload: Box<[u8; 1]> = Box::new([0]);
        let mut root_rows: Box<[[u32; 1]]> = Box::new([[0], [0], [0], [0], [0], [9]]);
        let mut chunks: Box<[OffsetPtr<u32>; 6]> = Box::new(core::array::from_fn(|_| OffsetPtr::null()));
        for (idx, row) in root_rows.iter_mut().enumerate() {
            chunks[idx].store(row.as_mut_ptr());
        }
        let root = &mut filter.v[0];
        root.table.v_dim = 6;
        root.table.h_dim = 1;
        root.table.values.store(chunks.as_mut_ptr());
        filter.v[1].data.store(payload.as_mut_ptr());
        (filter, payload)
    }

    #[test]
    fn single_attribute_query_maps_class_to_rule() {
        let (filter, mut payload) = fixture();
        payload[0] = 0;
        let mut slots = FilterSlots::with_capacity(2);
        let mut results = [0u32; 2];

        let filter_ref = &filter;
        // The leaf evaluator is a stand-in for a compiled attribute: it
        // stamps the payload byte as the packet's class.
        filter_query(
            filter_ref,
            1,
            |_leaf, data, out| {
                for slot in out.iter_mut() {
                    // SAFETY: fixture payload, one byte.
                    *slot = unsafe { *data } as u32;
                }
            },
            2,
            slots.for_count(2),
            &mut results,
        );
        assert_eq!(results, [0, 0]);

        payload[0] = 5;
        let mut slots = FilterSlots::with_capacity(2);
        let mut results = [0u32; 2];
        filter_query(
            &filter,
            1,
            |_leaf, data, out| {
                for slot in out.iter_mut() {
                    // SAFETY: fixture payload, one byte.
                    *slot = unsafe { *data } as u32;
                }
            },
            2,
            slots.for_count(2),
            &mut results,
        );
        assert_eq!(results, [9, 9]);
    }
}
