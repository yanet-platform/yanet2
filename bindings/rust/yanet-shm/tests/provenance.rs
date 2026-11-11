//! The provenance and ownership model under Miri.
//!
//! The cases hold under Stacked and Tree Borrows with strict provenance.

use core::mem::{offset_of, size_of};

use yanet_sdk_testing::{BlockArena, View};
use yanet_shm::{
    opaque::Opaque,
    rel::{ShmBox, ShmSlice},
};

/// A C-like object header: bytes C updates while Rust holds a reference to
/// the block around them.
///
/// Stands in for the C object that precedes a configuration body in a block
/// (a registry reference count, a memory-context sibling link).
#[repr(C)]
struct CHeader {
    /// Sibling link, rewritten by C.
    _link: u64,
    /// Counter, updated by C.
    counter: u64,
}

/// Offset of a counter C updates in its object header.
const COUNTER: usize = offset_of!(CHeader, counter);

/// A block as the builder lays it out: a C object, then the body.
#[repr(C)]
struct Block<B> {
    header: Opaque<CHeader>,
    body: B,
}

/// Offset of the body inside a block of the graph.
const BODY_OFFSET: usize = offset_of!(Block<Graph>, body);

#[repr(C)]
struct Item {
    value: u64,
}

#[repr(C)]
struct Graph {
    boxed: ShmBox<Item>,
    items: ShmSlice<Item>,
}

/// Writes a word at `addr`.
///
/// # Safety
///
/// `addr` must be an aligned word of a live block of `arena` that nothing
/// references.
unsafe fn write(arena: &'static BlockArena, addr: usize, value: u64) {
    // SAFETY: the caller states that the word is in a live block.
    unsafe { arena.root().ptr::<u64>(addr).write(value) }
}

/// Points the relative slot at `slot` to `target`.
///
/// # Safety
///
/// As for `write`, at `slot`.
unsafe fn point(arena: &'static BlockArena, slot: usize, target: usize) {
    // SAFETY: guaranteed by the caller.
    unsafe { write(arena, slot, target.wrapping_sub(slot) as u64) };
}

/// A graph laid out as the builder lays it out, in blocks of the arena: the
/// configuration block (the C object, then the body) with a boxed item 7 and
/// the items 0..4; returns the header.
fn build(arena: &'static BlockArena) -> usize {
    let header = arena.alloc(size_of::<Block<Graph>>()).as_ptr().addr();
    let body = header + BODY_OFFSET;
    let boxed = arena.alloc(size_of::<Item>()).as_ptr().addr();
    let items = arena.alloc(4 * size_of::<Item>()).as_ptr().addr();
    // SAFETY: words of the blocks just allocated.
    unsafe {
        write(arena, boxed, 7);
        for idx in 0..4 {
            write(arena, items + idx * size_of::<Item>(), idx as u64);
        }
    }
    // SAFETY: both slots are words of the configuration block just allocated.
    unsafe {
        point(arena, body + offset_of!(Graph, boxed), boxed);
        point(arena, body + offset_of!(Graph, items), items);
    }
    // SAFETY: the length word of the slice slot in the configuration block.
    unsafe { write(arena, body + offset_of!(Graph, items) + ShmSlice::<Item>::LEN_OFFSET, 4) };
    header
}

/// The graph's body, as a resolving context hands it out.
fn body(arena: &'static BlockArena, header: usize) -> &'static Graph {
    // SAFETY: a configuration block of the arena, valid by construction;
    // only the body is referenced, the C object stays opaque.
    unsafe { &(*arena.root().ptr::<Block<Graph>>(header)).body }
}

#[test]
fn test_provenance_resolution_through_the_root() {
    let arena = BlockArena::new();
    let header = build(arena);
    // SAFETY: the graph is valid and stays frozen for the test.
    let view = unsafe { View::new(arena.root()) };

    let config = body(arena, header);

    assert_eq!(7, config.boxed.get(&view).value);
    assert_eq!(
        vec![0, 1, 2, 3],
        config
            .items
            .as_slice(&view)
            .iter()
            .map(|item| item.value)
            .collect::<Vec<_>>()
    );
}

/// Reads a block's body while C writes into its object.
///
/// The reference is a function argument, so it is protected and covers the
/// whole block, header included, for the call.
#[inline(never)]
fn read_while_c_writes<'g>(block: &'g Block<Graph>, view: &View<'g>, write_c: impl FnOnce()) -> u64 {
    write_c();
    block.body.boxed.get(view).value
}

/// verifies that a C write to the opaque C object while Rust holds a
/// protected reference to the configuration block is not a conflict.
///
/// With a plain header type instead of `Opaque` Miri reports the write as UB.
#[test]
fn test_provenance_opaque_object_tolerates_c_writes() {
    let arena = BlockArena::new();
    let header = build(arena);
    let root = arena.root();
    // SAFETY: a configuration block valid by construction; the object bytes
    // are opaque.
    let block = unsafe { &*root.ptr::<Block<Graph>>(header) };
    // SAFETY: the graph is valid and stays frozen apart from the object.
    let view = unsafe { View::new(root) };

    let value = read_while_c_writes(block, &view, || {
        // SAFETY: the write models C updating a counter of the C object.
        unsafe { root.ptr::<u64>(header + COUNTER).write(1) }
    });

    assert_eq!(7, value);
}
