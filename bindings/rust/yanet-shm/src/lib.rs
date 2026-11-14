//! Boundary of the Rust module SDK shared by the dataplane and the control
//! plane.
//!
//! Holds the provenance root of the shared-memory mapping, the
//! relative-pointer field types, `Opaque`, the mutex, the exact-match index
//! and the `ShmLayout` trait with its leaf implementations. Pure Rust and
//! `no_std` without `alloc`: it binds no C header, so the crates that do build
//! on it.

#![no_std]

pub mod index;
pub mod mutex;
pub mod opaque;
pub mod rel;
pub mod resolve;
pub mod root;
pub mod shm;
mod spinlock;
