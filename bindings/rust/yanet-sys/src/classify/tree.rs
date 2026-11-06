//! A classifier as one tree: each joint, rule table and rule map holds the
//! classifiers whose classes it reads.
//!
//! C joins two stages into a table sized by their class counts, and its
//! queries pass the table only the classes of those stages
//! (`lib/classify/query.h`). The tree keeps that promise by construction: a
//! node is written only by a [`Scope`](crate::cp::Scope), together with the
//! very nodes it was joined or decoded from, and its fields are private, so a
//! class never leaves the SDK and no lookup checks one. Every read is the C
//! one of the shim, which a plugin built with cross-language LTO inlines.

use core::{
    convert::Infallible,
    mem::{offset_of, size_of},
};

use yanet_shm::{rel::ShmBox, resolve::Resolve, shm::ShmLayout};

use super::{Addr4, Joint, LineAttr, Net4Attr, Net6Attr, PortAttr, Rule, RuleMap, RuleTable, raw};
use crate::{
    bindings,
    cp::{Build, MemoryContext, Owned, Owner, Refusal},
};

/// A classifier of a tree: an attribute, or a joint of two classifiers.
///
/// Sealed: every node is one of this module's, so each class it reports is a
/// class of the stage it was compiled or joined with.
pub trait Node: Build + sealed::Sealed {
    /// What the node classifies: an attribute's value, or the pair of keys
    /// of a joint.
    type Key: Copy;

    /// The class of a key.
    #[doc(hidden)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, key: Self::Key) -> u32;
}

// SAFETY (every lookup below): the classifier belongs to the configuration
// published and frozen for 'g (`Resolve` contract) and was built whole by its
// C build, together with the nodes of the classes it reads, whose classes its
// dimensions were sized by; C only reads it.

impl Node for Net4Attr {
    type Key = Addr4;

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, Addr4(addr): Addr4) -> u32 {
        // SAFETY: see above; the match covers the whole address space.
        unsafe { bindings::yanet_sys_classify_net4_class(raw(cx, self), addr) }
    }
}

impl Node for Net6Attr {
    type Key = [u8; 16];

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, addr: [u8; 16]) -> u32 {
        // SAFETY: see above; C reads the sixteen bytes of the address.
        unsafe { bindings::yanet_sys_classify_net6_class(raw(cx, self), addr.as_ptr()) }
    }
}

impl Node for PortAttr {
    type Key = u16;

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, port: u16) -> u32 {
        // SAFETY: see above; the line spans the whole port domain.
        unsafe { bindings::yanet_sys_classify_port_class(raw(cx, self), port) }
    }
}

impl Node for LineAttr {
    type Key = u8;

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, value: u8) -> u32 {
        // SAFETY: see above; the line spans the whole byte domain.
        unsafe { bindings::yanet_sys_classify_line_class(raw(cx, self), value) }
    }
}

/// The joint of two classifiers: the class of each pair of their classes.
///
/// Joined only with its stage, by
/// [`Scope::join_branches`](crate::cp::Scope::join_branches).
#[repr(C)]
pub struct Join<L, R> {
    table: Joint,
    left: ShmBox<L>,
    right: ShmBox<R>,
}

/// The first rule each pair of classes of two classifiers matches.
///
/// Joined by [`Scope::branch_rules`](crate::cp::Scope::branch_rules).
#[repr(C)]
pub struct Rules<L, R> {
    table: RuleTable,
    left: ShmBox<L>,
    right: ShmBox<R>,
}

/// The first rule each class of a classifier matches.
///
/// Decoded by [`Scope::decode_branch`](crate::cp::Scope::decode_branch).
#[repr(C)]
pub struct Decode<N> {
    map: RuleMap,
    input: ShmBox<N>,
}

impl<L: Node, R: Node> Node for Join<L, R> {
    type Key = (L::Key, R::Key);

    #[inline(always)]
    fn class<'g>(&'g self, cx: &impl Resolve<'g>, (left, right): Self::Key) -> u32 {
        let (left, right) = (self.left.get(cx).class(cx, left), self.right.get(cx).class(cx, right));
        // SAFETY: see above; the classes are of the stages the joint was
        // joined from.
        unsafe { bindings::yanet_sys_classify_joint_class(raw(cx, &self.table), left, right) }
    }
}

impl<L: Node, R: Node> Rules<L, R> {
    /// The first rule a key matches, [`Rule::NONE`] for none.
    #[inline(always)]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, (left, right): (L::Key, R::Key)) -> Rule {
        let (left, right) = (self.left.get(cx).class(cx, left), self.right.get(cx).class(cx, right));
        // SAFETY: see above; the classes are of the stages the table was
        // joined from.
        Rule(unsafe { bindings::yanet_sys_classify_rule_table_rule(raw(cx, &self.table), left, right) })
    }
}

impl<N: Node> Decode<N> {
    /// The first rule a key matches, [`Rule::NONE`] for none.
    #[inline(always)]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, key: N::Key) -> Rule {
        let class = self.input.get(cx).class(cx, key);
        // SAFETY: see above; the class is of the stage the map was decoded
        // from.
        Rule(unsafe { bindings::yanet_sys_classify_rule_map_rule(raw(cx, &self.map), class) })
    }
}

// The nodes leave no padding: a C table, then the offset words of the boxes.
const _: () = {
    type Edge = ShmBox<u8>;
    assert!(size_of::<Join<u8, u8>>() == size_of::<Joint>() + 2 * size_of::<Edge>());
    assert!(size_of::<Rules<u8, u8>>() == size_of::<RuleTable>() + 2 * size_of::<Edge>());
    assert!(size_of::<Decode<u8>>() == size_of::<RuleMap>() + size_of::<Edge>());
    assert!(offset_of!(Join<u8, u8>, table) == 0);
    assert!(offset_of!(Rules<u8, u8>, table) == 0);
    assert!(offset_of!(Decode<u8>, map) == 0);
};

/// The shared-memory impls of the nodes that hold their inputs.
macro_rules! holders {
    ($($name:ident<$($param:ident),*> { $table:ident: $raw:ty, $free:ident; $($edge:ident: $ty:ident),* })*) => {$(
        // SAFETY: a `#[repr(C)]` struct of an opaque C object, which accepts
        // every pattern, and offset words, without padding (asserted above);
        // the C object leads, so the node has its alignment, and dropping is
        // a no-op.
        unsafe impl<$($param: ShmLayout),*> ShmLayout for $name<$($param),*> {}

        // SAFETY: written only by the scope's joins and decodes, which put
        // the C object and the owned edges whole; freeing returns what the C
        // build allocated in its context, then the inputs, newest first.
        unsafe impl<$($param: Build),*> Build for $name<$($param),*> {
            type Parts<'b, W: Owner> = Infallible;

            unsafe fn write<W: Owner>(
                parts: Infallible,
                _: &MemoryContext<'_, W>,
                _: usize,
            ) -> Result<(), Refusal> {
                match parts {}
            }

            unsafe fn free<W: Owner>(addr: usize, memory: &MemoryContext<'_, W>) {
                // SAFETY: the node was written whole (caller contract), its C
                // object at its start.
                unsafe {
                    bindings::$free(memory.root().ptr::<$raw>(addr + offset_of!(Self, $table)));
                    $(<ShmBox<$ty> as Build>::free::<W>(addr + offset_of!(Self, $edge), memory);)*
                }
            }
        }
    )*};
}

holders! {
    Join<L, R> { table: bindings::value_table, yanet_sys_classify_joint_free; right: R, left: L }
    Rules<L, R> { table: bindings::rule_table, yanet_sys_classify_rule_table_free; right: R, left: L }
    Decode<N> { map: bindings::vline, yanet_sys_classify_rule_map_free; input: N }
}

mod sealed {
    pub trait Sealed {}

    impl Sealed for super::Net4Attr {}

    impl Sealed for super::Net6Attr {}

    impl Sealed for super::PortAttr {}

    impl Sealed for super::LineAttr {}

    impl<L, R> Sealed for super::Join<L, R> {}
}

/// Writes an owned edge of a node whose C object a build has just written.
///
/// # Safety
///
/// The edge is an unwritten offset word of a node in a block of the memory
/// context's build, whose C object is written whole; the part is of the same
/// build.
#[doc(hidden)]
pub unsafe fn link<W: Owner, T: Build>(memory: &MemoryContext<'_, W>, edge: usize, part: Owned<'_, T, W>) {
    // SAFETY: the edge is an aligned, unwritten offset word of the node
    // (caller contract); a box write only stores the offset.
    let written = unsafe { <ShmBox<T> as Build>::write::<W>(part, memory, edge) };
    debug_assert!(written.is_ok(), "a box write does not fail");
}

impl<L, R> Join<L, R> {
    /// Offsets of the left and the right edge.
    #[doc(hidden)]
    pub const EDGES: (usize, usize) = (offset_of!(Self, left), offset_of!(Self, right));
}

impl<L, R> Rules<L, R> {
    /// Offsets of the left and the right edge.
    #[doc(hidden)]
    pub const EDGES: (usize, usize) = (offset_of!(Self, left), offset_of!(Self, right));
}

impl<N> Decode<N> {
    /// Offset of the edge of the input.
    #[doc(hidden)]
    pub const EDGE: usize = offset_of!(Self, input);
}
