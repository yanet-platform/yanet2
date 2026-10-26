//! LPM lookup over the block arena, under Miri.
//!
//! The LPM is laid out by hand as the C insert lays it out.

use core::mem::{offset_of, size_of};

// The workspace denies unused crate dependencies; this test names no crate it
// imports `as _`.
use yanet_packet as _;
use yanet_sdk_testing::{BlockArena, View};
use yanet_shm as _;
use yanet_sys::{bindings, common::lpm::Lpm4};

fn write(arena: &'static BlockArena, addr: usize, value: u64) {
    // SAFETY: a word of a live block of the arena; nothing references it.
    unsafe { arena.root().ptr::<u64>(addr).write(value) }
}

/// Slots of one LPM page.
const PAGE_SLOTS: usize = 256;
/// Bytes of one LPM page.
const PAGE: usize = PAGE_SLOTS * 8;

/// Leaf slot of `value`, as `LPM_VALUE_SET` stores it.
const fn leaf(value: u32) -> u64 {
    ((value << 1) | bindings::LPM_VALUE_FLAG) as u64
}

/// Points the relative slot at `slot` to `target`.
fn point(arena: &'static BlockArena, slot: usize, target: usize) {
    write(arena, slot, target.wrapping_sub(slot) as u64);
}

/// Addresses of a hand-built LPM.
struct LpmAt {
    directory: usize,
    chunk: usize,
}

impl LpmAt {
    fn page(&self, idx: usize) -> usize {
        self.chunk + idx * PAGE
    }
}

/// Lays an IPv4 LPM at `lpm` out as the C insert lays it out: one
/// directory entry, one chunk, three pages in use. `10.0.0.0/16` maps to 5
/// on page 1, `10.1.0.0/16` reaches page 2, whose every slot maps to 9;
/// every other slot is the invalid mark.
fn fill_lpm(arena: &'static BlockArena, lpm: usize) -> LpmAt {
    let at = LpmAt {
        directory: arena.alloc(8).as_ptr().addr(),
        chunk: arena.alloc(bindings::LPM_CHUNK_SIZE as usize * PAGE).as_ptr().addr(),
    };
    for slot in 0..3 * PAGE_SLOTS {
        write(arena, at.chunk + slot * 8, u64::MAX);
    }
    point(arena, at.page(0) + 10 * 8, at.page(1));
    write(arena, at.page(1), leaf(5));
    point(arena, at.page(1) + 8, at.page(2));
    for slot in 0..PAGE_SLOTS {
        write(arena, at.page(2) + slot * 8, leaf(9));
    }
    point(arena, at.directory, at.chunk);
    point(arena, lpm + offset_of!(bindings::lpm, pages), at.directory);
    write(arena, lpm + offset_of!(bindings::lpm, page_count), 3);
    at
}

/// verifies that the lookup walks a hand-built LPM's pages to the leaf of
/// each key, or to none.
#[test]
fn test_lpm_lookup_walks_pages() {
    let arena = BlockArena::new();
    let addr = arena.alloc(size_of::<Lpm4>()).as_ptr().addr();
    fill_lpm(arena, addr);

    // SAFETY: the LPM is laid out as C lays it out and stays frozen for the
    // test.
    let (view, lpm) = unsafe { (View::new(arena.root()), &*arena.root().ptr::<Lpm4>(addr)) };
    assert_eq!(
        [Some(5), Some(9), None, None],
        [[10, 0, 7, 7], [10, 1, 3, 4], [11, 0, 0, 0], [10, 2, 0, 0]].map(|key| lpm.lookup(&view, &key))
    );
}
