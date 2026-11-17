//! LPM lookup over the block arena, under Miri.
//!
//! The LPM is laid out by hand as the C insert lays it out.

use core::{
    mem::{offset_of, size_of},
    net::{Ipv4Addr, Ipv6Addr},
};

use yanet_sdk_testing::{BlockArena, View};
use yanet_sys::{
    bindings,
    common::lpm::{Lpm, Lpm4, Lpm6},
};

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

/// Lays an LPM out at an address as the C insert lays it out.
///
/// One directory entry, one chunk, three pages in use. Keys that start with
/// bytes 10, 0 map to 5 on page 1, keys that start with 10, 1 reach page 2,
/// whose every slot maps to 9; every other slot is the invalid mark.
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

/// A hand-built LPM of the requested key width and the view that resolves it.
///
/// Its pages end every key on a leaf within three bytes, so they make a table
/// of any key of three bytes or more.
fn hand_built<const K: usize>(arena: &'static BlockArena) -> (View<'static>, &'static Lpm<K>) {
    let addr = arena.alloc(size_of::<Lpm<K>>()).as_ptr().addr();
    fill_lpm(arena, addr);
    // SAFETY: the LPM is laid out as C lays it out and stays frozen for the
    // test.
    unsafe { (View::new(arena.root()), &*arena.root().ptr::<Lpm<K>>(addr)) }
}

/// verifies that the lookup walks a hand-built LPM's pages to the leaf of
/// each key, or to none.
#[test]
fn test_lpm_lookup_walks_pages() {
    let (view, lpm) = hand_built::<4>(BlockArena::new());
    assert_eq!(
        [Some(5), Some(9), None, None],
        [[10, 0, 7, 7], [10, 1, 3, 4], [11, 0, 0, 0], [10, 2, 0, 0]]
            .each_ref()
            .map(|key| lpm.lookup(&view, key))
    );
}

#[test]
fn test_lpm_lookup_by_mut_array_finds_what_a_shared_one_finds() {
    let (view, lpm) = hand_built::<4>(BlockArena::new());
    let mut keys = [[10, 0, 7, 7], [10, 1, 3, 4], [11, 0, 0, 0], [10, 2, 0, 0]];

    let by_shared = keys.each_ref().map(|key| lpm.lookup(&view, key));
    assert_eq!(by_shared, keys.each_mut().map(|key| lpm.lookup(&view, key)));
}

#[test]
fn test_lpm4_lookup_by_address_finds_what_its_octets_find() {
    let (view, lpm): (_, &Lpm4) = hand_built(BlockArena::new());
    let keys = [
        Ipv4Addr::new(10, 0, 7, 7),
        Ipv4Addr::new(10, 1, 3, 4),
        Ipv4Addr::new(11, 0, 0, 0),
        Ipv4Addr::new(10, 2, 0, 0),
    ];

    let by_octets = keys.map(|key| lpm.lookup(&view, key.octets()));
    // The keys reach both leaves and both misses.
    assert_eq!([Some(5), Some(9), None, None], by_octets);
    assert_eq!(by_octets, keys.map(|key| lpm.lookup(&view, key)));
}

#[test]
fn test_lpm6_lookup_by_address_finds_what_its_octets_find() {
    let (view, lpm): (_, &Lpm6) = hand_built(BlockArena::new());
    let keys = [
        Ipv6Addr::new(0x0a00, 0x0707, 0, 0, 0, 0, 0, 1),
        Ipv6Addr::new(0x0a01, 0x0304, 0, 0, 0, 0, 0, 1),
        Ipv6Addr::new(0x0b00, 0, 0, 0, 0, 0, 0, 0),
        Ipv6Addr::new(0x0a02, 0, 0, 0, 0, 0, 0, 0),
    ];

    let by_octets = keys.map(|key| lpm.lookup(&view, key.octets()));
    // The keys reach both leaves and both misses.
    assert_eq!([Some(5), Some(9), None, None], by_octets);
    assert_eq!(by_octets, keys.map(|key| lpm.lookup(&view, key)));
}
