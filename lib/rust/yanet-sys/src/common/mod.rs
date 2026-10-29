//! Bindings of `common/`: the LPM type with its lookup and the range it is
//! built from.
//!
//! The table is built by the C routines of `common/`, and the shared-memory
//! alignment bound is checked against the C block allocator's.

pub mod lpm;

const _: () = assert!(yanet_shm::shm::MAX_ALIGN == crate::bindings::MEMORY_BLOCK_MAX_ALIGN as usize);
