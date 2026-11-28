//! The C bindings of the Rust module SDK, one module per C library.
//!
//! Holds the one bindgen output of the C headers (generated here only, so one
//! C struct is one Rust type). The modules mirror the C tree; a C structure a
//! module configuration embeds is a hand-written `#[repr(C)]` mirror whose
//! size, alignment and field offsets are asserted against the bindgen type.
//! `common` mirrors `common/`, `classify` binds `lib/classify`. `no_std`
//! without `alloc`: module crates and dataplane plugins depend on it.

#![no_std]

#[allow(
    non_camel_case_types,
    non_upper_case_globals,
    non_snake_case,
    dead_code,
    missing_docs,
    unnecessary_transmutes,
    unsafe_op_in_unsafe_fn,
    unused_qualifications,
    clippy::all,
    clippy::ptr_as_ptr,
    clippy::undocumented_unsafe_blocks,
    reason = "bindgen generates this module from the C headers"
)]
pub mod bindings {
    include!(concat!(env!("OUT_DIR"), "/bindings.rs"));
}

/// The layout description of an SDK field type: its kind, its members (its
/// words, and the C table of a classifier node), each at Rust's offset, and its
/// type arguments.
///
/// Each member's type is the one it has in `$shape`, the field type over
/// placeholder type arguments, so the description is generic.
macro_rules! edge_layout {
    ($kind:literal, $shape:ty, [$($member:ident),*], [$($arg:ty),*]) => {
        const LAYOUT: &'static yanet_shm::layout::Node =
            &yanet_shm::layout::Node::of::<Self>(yanet_shm::layout::Kind::Edge {
                kind: $kind,
                members: &[$(yanet_shm::layout::Field {
                    name: stringify!($member),
                    offset: core::mem::offset_of!(Self, $member),
                    ty: yanet_shm::layout::member_type(|shape: &$shape| &shape.$member),
                }),*],
                args: &[$(<$arg as yanet_shm::shm::ShmLayout>::LAYOUT),*],
            });
    };
}

/// The layout description of a mirror of the C struct of that tag name.
macro_rules! c_layout {
    ($name:expr) => {
        const LAYOUT: &'static yanet_shm::layout::Node =
            &yanet_shm::layout::Node::of::<Self>(yanet_shm::layout::Kind::C { name: $name });
    };
}

pub mod classify;
pub mod common;
pub mod counters;
pub mod dp;
