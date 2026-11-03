//! The object trait: one impl per object type binds its type name and its
//! configuration.

use core::mem::{align_of, size_of};

use yanet_shm::opaque::Opaque;

use crate::{bindings, cp::Build};

/// A shared object written in Rust: named state that outlives the module
/// configurations linking it, as C's `cp_object`.
///
/// An object is built once by the control plane, published into a
/// generation by name, and replaced by publishing another of the same type
/// and name; module configurations come and go around it. One impl feeds both
/// the object's plugin, which registers the type with the dataplane, and its
/// api crate, which builds it.
///
/// Not yet: a per-worker commit hook or prepared buffer of the type, and
/// links from a module configuration to an object, which the dataplane would
/// reach it through. An object type ships in a plugin of its own, as every
/// plugin exports one `yanet_module_abi_version`.
pub trait Object: Sized + 'static {
    /// Object type name, as the dataplane registers the type and as the
    /// object's plugin exports it.
    const TYPE: &'static str;
    /// Configuration body, the object's state; the control plane builds it
    /// with [`Build`] and may read it while it is published, so it is `Sync`.
    type Config: Build + Sync;
}

/// The root block of an object: the C object header, then the body the
/// object type declares.
///
/// The header is C's `cp_object`, which the control plane initialises and
/// finalises; its bytes stay opaque to Rust. Its entity base sits at offset
/// zero, as a module header's does, so the memory context of the build is at
/// the same offset in both.
#[repr(C)]
pub struct ObjectConfig<B> {
    header: Opaque<bindings::cp_object>,
    body: B,
}

impl<B> ObjectConfig<B> {
    /// Offset of the body inside the block.
    pub const BODY_OFFSET: usize = core::mem::offset_of!(Self, body);

    /// Object body.
    #[inline(always)]
    pub fn body(&self) -> &B {
        &self.body
    }
}

const _: () = assert!(core::mem::offset_of!(ObjectConfig<u64>, header) == 0);
const _: () = assert!(size_of::<Opaque<bindings::cp_object>>() == size_of::<bindings::cp_object>());
const _: () = assert!(align_of::<Opaque<bindings::cp_object>>() == align_of::<bindings::cp_object>());
