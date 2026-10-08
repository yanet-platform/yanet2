//! Class-join table and decoder line mirrors over shared memory.

use crate::{memory::MemoryContext, offset::OffsetPtr};

/// Mirror of `struct value_table` (`common/value.h`).
///
/// A rectangular table of classes: rows are chunked per `v` index, each
/// chunk holding `h_dim` entries, so a lookup adds and never multiplies.
/// Immutable after the control-plane compiler finishes.
#[repr(C)]
#[derive(Default)]
pub struct ValueTable {
    pub memory_context: OffsetPtr<MemoryContext>,
    pub v_dim: u32,
    pub h_dim: u32,
    /// Offset pointer to an array of `v_dim` offset pointers, one chunk
    /// of `h_dim` u32 entries per row.
    pub values: OffsetPtr<OffsetPtr<u32>>,
}

/// Read one table cell, mirroring `value_table_get`.
pub fn value_table_get(table: &ValueTable, v_idx: u32, h_idx: u32) -> u32 {
    debug_assert!(v_idx < table.v_dim);
    debug_assert!(h_idx < table.h_dim);
    // SAFETY: values and the chunk pointers are set at compile time and
    // cleared only by value_table_free, which never races a lookup, so
    // they are never NULL on the query path.
    unsafe {
        let chunks = table.values.resolve_non_null();
        let chunk_field = &*chunks.add(v_idx as usize);
        *chunk_field.resolve_non_null().add(h_idx as usize)
    }
}

/// Mirror of `struct vline` (`common/value.h`).
///
/// A single-dimensional decoder line: one contiguous chunk of classes.
#[repr(C)]
#[derive(Default)]
pub struct Vline {
    pub memory_context: OffsetPtr<MemoryContext>,
    pub size: u32,
    pub values: OffsetPtr<u32>,
}

/// Read one line entry, mirroring `vline_get`.
pub fn vline_get(line: &Vline, idx: u32) -> u32 {
    debug_assert!(idx < line.size);
    // SAFETY: the values chunk is set at compile time and never NULL on
    // the query path.
    unsafe { *line.values.resolve_non_null().add(idx as usize) }
}

const _: () = assert!(core::mem::size_of::<ValueTable>() == 24);
const _: () = assert!(core::mem::size_of::<Vline>() == 24);

#[cfg(test)]
mod tests {
    use std::prelude::v1::*;

    use super::*;

    #[test]
    fn table_reads_rows_through_chunks() {
        let mut rows: Box<[[u32; 2]]> = Box::new([[10, 11], [20, 21]]);
        let mut chunks: Box<[OffsetPtr<u32>; 2]> = Box::new([OffsetPtr::null(), OffsetPtr::null()]);
        chunks[0].store(rows[0].as_mut_ptr());
        chunks[1].store(rows[1].as_mut_ptr());

        let mut table = ValueTable {
            v_dim: 2,
            h_dim: 2,
            ..ValueTable::default()
        };
        table.values.store(chunks.as_mut_ptr());

        assert_eq!(value_table_get(&table, 0, 0), 10);
        assert_eq!(value_table_get(&table, 0, 1), 11);
        assert_eq!(value_table_get(&table, 1, 0), 20);
        assert_eq!(value_table_get(&table, 1, 1), 21);
    }

    #[test]
    fn line_reads_contiguous_values() {
        let mut values: Box<[u32; 3]> = Box::new([7, 8, 9]);
        let mut line = Vline { size: 3, ..Vline::default() };
        line.values.store(values.as_mut_ptr());
        assert_eq!(vline_get(&line, 0), 7);
        assert_eq!(vline_get(&line, 2), 9);
    }
}
