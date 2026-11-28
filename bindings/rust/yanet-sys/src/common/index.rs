//! The exact-match index as a shared-memory field type.
//!
//! [`HashIndex`] embeds a memory context of `common/memory.h`, opaque as the
//! LPM's is, so a reference to it stays sound while C rewrites the context's
//! sibling link. The control plane opens the context under the
//! configuration's, named `hash_index`, and takes the block of the slots from
//! it, so the memory diagnostics of C show the index as a node of its own. The
//! lookup is pure Rust. Unlike C `hash_index`, a slot holds its key, so a
//! lookup reads no other array.

use core::{
    mem::{align_of, offset_of, size_of},
    net::Ipv4Addr,
};

use yanet_shm::{
    index::{HashIndexKey, HashIndexRef, HashIndexSlot},
    layout::{Field, Kind, Node},
    opaque::Opaque,
    rel::ShmSlice,
    resolve::Resolve,
    shm::{MAX_ALIGN, ShmLayout},
};

use crate::bindings;

/// An exact-match index of keys of type `K`, as a configuration holds it: the
/// position each key names in an array of the author's.
#[repr(C)]
pub struct HashIndex<K> {
    /// The memory context of the block of the slots. C rewrites its sibling
    /// link after publish when another context under the same parent is
    /// finalised.
    _memory_context: Opaque<bindings::memory_context>,
    slots: ShmSlice<HashIndexSlot<K>>,
}

const _: () = {
    type Rust = HashIndex<Ipv4Addr>;
    assert!(offset_of!(Rust, _memory_context) == 0);
    assert!(offset_of!(Rust, slots) == size_of::<bindings::memory_context>());
    // The fields leave no padding.
    assert!(
        size_of::<Rust>() == size_of::<bindings::memory_context>() + size_of::<ShmSlice<HashIndexSlot<Ipv4Addr>>>()
    );
    assert!(align_of::<Rust>() <= MAX_ALIGN);
};

impl<K: HashIndexKey> HashIndex<K> {
    /// The index as a handler call reads it.
    pub fn resolve<'g>(&'g self, cx: &impl Resolve<'g>) -> HashIndexRef<'g, K> {
        HashIndexRef::new(self.slots.as_slice(cx))
    }
}

// SAFETY: a `#[repr(C)]` value of the opaque C context and the offset and
// count words of a slice, sizes and offsets asserted with no padding. Every
// pattern is valid: Rust never reads the context, and the slice is two words.
// Dropping is a no-op and the alignment is 8.
unsafe impl<K: ShmLayout> ShmLayout for HashIndex<K> {
    // The context as its C view and the slice of the slots.
    //
    // The description takes no type argument: a slot holds its key by value,
    // so each key type gives the index another shape, and a module whose
    // indices hold two key types fails generation.
    const LAYOUT: &'static Node = &Node::of::<Self>(Kind::Edge {
        kind: "hash_index",
        members: &[
            Field {
                name: "memory_context",
                offset: offset_of!(Self, _memory_context),
                ty: &Node::of::<bindings::memory_context>(Kind::C { name: "memory_context" }),
            },
            Field {
                name: "slots",
                offset: offset_of!(Self, slots),
                ty: <ShmSlice<HashIndexSlot<K>>>::LAYOUT,
            },
        ],
        args: &[],
    });
}
