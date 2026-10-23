//! The provenance and ownership model under Miri.
//!
//! The positive cases hold under Stacked and Tree Borrows with strict
//! provenance. The ignored cases are expected outcomes the Miri script runs
//! one by one: each breaks one rule of the model and must be reported as
//! Undefined Behavior (the slot-reference case only under Stacked Borrows,
//! which bounds a reference's provenance by its range).

use core::mem::{offset_of, size_of};

use yanet_sdk_testing::{BlockArena, CHeader, View};
use yanet_shm::{
    opaque::Opaque,
    rel::{ShmBox, ShmSlice},
};

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

fn write(arena: &'static BlockArena, addr: usize, value: u64) {
    // SAFETY: a word of a live block of the arena; nothing references it.
    unsafe { arena.root().ptr::<u64>(addr).write(value) }
}

/// Points the relative slot at `slot` to `target`.
fn point(arena: &'static BlockArena, slot: usize, target: usize) {
    write(arena, slot, target.wrapping_sub(slot) as u64);
}

/// A graph laid out as the builder lays it out, each owned block an arena
/// block of its own: the configuration block (the C object, then the body)
/// with a boxed item 7 and the items 0..4; returns the header.
fn build(arena: &'static BlockArena) -> usize {
    let header = arena.alloc(size_of::<Block<Graph>>()).as_ptr().addr();
    let body = header + BODY_OFFSET;
    let boxed = arena.alloc(size_of::<Item>()).as_ptr().addr();
    write(arena, boxed, 7);
    let items = arena.alloc(4 * size_of::<Item>()).as_ptr().addr();
    for idx in 0..4 {
        write(arena, items + idx * size_of::<Item>(), idx as u64);
    }
    point(arena, body + offset_of!(Graph, boxed), boxed);
    point(arena, body + offset_of!(Graph, items), items);
    write(arena, body + offset_of!(Graph, items) + ShmSlice::<Item>::LEN_OFFSET, 4);
    header
}

/// The graph's body, as a resolving context hands it out.
fn body(arena: &'static BlockArena, header: usize) -> &'static Graph {
    // SAFETY: a configuration block of the arena, valid by construction;
    // only the body is referenced, the C object stays opaque.
    unsafe { &(*arena.root().ptr::<Block<Graph>>(header)).body }
}

fn slot_target(arena: &'static BlockArena, slot: usize) -> usize {
    // SAFETY: a word of a live block of the arena.
    let offset = unsafe { arena.root().ptr::<i64>(slot).read() };
    slot.wrapping_add_signed(offset as isize)
}

#[test]
fn test_provenance_resolution_through_block_roots() {
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

/// verifies that a root kept past the scope of its arena handle stays
/// usable: arenas are never dropped, so the root's block table is live.
#[test]
fn test_provenance_root_outlives_arena_handle() {
    let root = {
        let arena = BlockArena::new();
        arena.root()
    };

    let ptr = root.ptr::<u8>(0x1000);

    assert_eq!(0x1000, ptr.addr());
}

/// Expected UB: an edge resolved after its owned block was freed; the
/// address belongs to no live block, so Miri reports the access as out of
/// bounds of the fallback allocation.
#[test]
#[ignore = "expected UB, run by cargo xtask miri"]
fn test_ub_freed_block() {
    let arena = BlockArena::new();
    let header = build(arena);
    let config = body(arena, header);
    arena.free(slot_target(arena, core::ptr::from_ref(&config.boxed).addr()));
    // SAFETY: deliberately broken: the configuration lost a block.
    let view = unsafe { View::new(arena.root()) };

    let value = config.boxed.get(&view).value;

    assert_eq!(7, value);
}

/// Expected UB: a slice whose length runs past its owned block is resolved
/// as stored, so reading it crosses into another allocation.
#[test]
#[ignore = "expected UB, run by cargo xtask miri"]
fn test_ub_cross_block_read() {
    let arena = BlockArena::new();
    let header = build(arena);
    let body_addr = header + BODY_OFFSET;
    // Deliberately corrupts the slice length of the valid graph.
    write(
        arena,
        body_addr + offset_of!(Graph, items) + ShmSlice::<Item>::LEN_OFFSET,
        64,
    );
    // SAFETY: deliberately broken: the graph is no longer valid.
    let view = unsafe { View::new(arena.root()) };

    let sum: u64 = body(arena, header)
        .items
        .as_slice(&view)
        .iter()
        .map(|item| item.value)
        .sum();

    assert!(sum > 0);
}

/// Expected UB: a plain reference to the whole C object, not `Opaque`, held
/// across a C write to its bytes.
#[test]
#[ignore = "expected UB, run by cargo xtask miri"]
fn test_ub_reference_over_c_mutated_bytes() {
    let arena = BlockArena::new();
    let header = build(arena);
    let root = arena.root();
    // SAFETY: deliberately wrong: a frozen view of bytes C mutates.
    let plain = unsafe { &*root.ptr::<CHeader>(header) };

    // SAFETY: the write models C updating a counter of the C object.
    unsafe { root.ptr::<u64>(header + COUNTER).write(1) };

    assert_eq!(1, plain.counter);
}

/// Expected UB under Stacked Borrows: a target whose provenance comes from
/// the slot reference, not from the shared-memory root.
#[test]
#[ignore = "expected UB under Stacked Borrows, run by cargo xtask miri"]
fn test_ub_slot_reference_provenance() {
    let arena = BlockArena::new();
    let image = arena.alloc(256).as_ptr().addr();
    let root = arena.root();
    // SAFETY: a fresh block: a box slot at 0 pointing at an item at 64.
    unsafe {
        root.ptr::<i64>(image).write(64);
        root.ptr::<u64>(image + 64).write(7);
    }
    // SAFETY: every bit pattern is a valid slot.
    let slot = unsafe { &*root.ptr::<ShmBox<Item>>(image) };

    let slot = core::ptr::from_ref(slot);
    // SAFETY: the slot's own word, read through its reference.
    let offset = unsafe { slot.cast::<isize>().read() };
    let target = slot.cast::<u8>().wrapping_offset(offset).cast::<Item>();
    // SAFETY: deliberately wrong provenance: the slot reference covers its
    // own 8 bytes only.
    let value = unsafe { (*target).value };

    assert_eq!(7, value);
    assert_eq!(8, size_of::<ShmBox<Item>>());
}
