//! LPM lookup over the block arena, under Miri.
//!
//! The trie is laid out by hand as the C build lays it out.

use core::mem::{offset_of, size_of};

// The workspace denies unused crate dependencies; this test names no crate it
// imports `as _`.
use netip as _;
use yanet_packet as _;
use yanet_sdk_testing::{BlockArena, View};
use yanet_shm as _;
use yanet_sys::{
    bindings,
    common::lpm::{Lpm4, Lpm6},
};

fn write<T>(arena: &'static BlockArena, addr: usize, value: T) {
    // SAFETY: a word of a live block of the arena; nothing references it.
    unsafe { arena.root().ptr::<T>(addr).write(value) }
}

/// Entry of a leaf of `value`, as the C build stores it.
const fn leaf(value: u32) -> u32 {
    (value << 1) | bindings::FAST_LPM_ENTRY_LEAF
}

/// Entry of a link from the node at `node` to a child of `kind` at `child`.
fn link(node: usize, child: usize, kind: u32) -> u32 {
    let units = (child as isize - node as isize) / bindings::FAST_LPM_NODE_ALIGN as isize;
    ((units as i32 as u32) << 3) | kind
}

/// Bytes of one full node.
const NODE: usize = 256 * size_of::<u32>();

/// A full node at `node` with every entry `entry`.
fn fill(arena: &'static BlockArena, node: usize, entry: u32) {
    for slot in 0..256 {
        write(arena, node + slot * size_of::<u32>(), entry);
    }
}

/// Points the root slot of the LPM at `lpm` to the node at `root`.
fn set_root(arena: &'static BlockArena, lpm: usize, root: usize) {
    let slot = lpm + offset_of!(bindings::fast_lpm, root);
    write(arena, slot, root.wrapping_sub(slot) as u64);
}

/// Lays an IPv4 trie at `lpm` out as the C build lays it out: `10.0.0.0/16`
/// maps to 5 through a second full node, and below `10.1.0.0/16`, which
/// maps to 9, a narrow bucket maps `10.1.3.0/24` to 11; every other key maps
/// to the invalid value.
fn lay_ipv4(arena: &'static BlockArena, lpm: usize) {
    let nodes = arena.alloc(4 * NODE).as_ptr().addr();
    let (root, second, bucket) = (nodes, nodes + NODE, nodes + 2 * NODE);
    fill(arena, root, u32::MAX);
    fill(arena, second, u32::MAX);
    write(arena, root + 10 * 4, link(root, second, bindings::FAST_LPM_KIND_FULL));
    write(arena, second, leaf(5));
    write(arena, second + 4, link(second, bucket, bindings::FAST_LPM_KIND_NARROW));
    // Values in reverse order: the count of bounds above the key picks one.
    for (idx, value) in [9, 9, 9, 11, 9].into_iter().enumerate() {
        write(arena, bucket + 4 * idx, leaf(value));
    }
    let bounds = [0x0a01_0300u32, 0x0a01_0400, u32::MAX, u32::MAX];
    for (idx, bound) in bounds.into_iter().enumerate() {
        let at = bucket + bindings::FAST_LPM_NARROW_BOUNDS_OFFSET as usize + 4 * idx;
        write(arena, at, bound ^ 0x8000_0000);
    }
    set_root(arena, lpm, root);
}

/// verifies that the lookup walks a hand-built IPv4 trie through its full
/// nodes and narrow bucket to the leaf of each key, or to none.
#[test]
fn test_lpm_lookup_walks_nodes_and_narrow_bucket() {
    let arena = BlockArena::new();
    let addr = arena.alloc(size_of::<Lpm4>()).as_ptr().addr();
    lay_ipv4(arena, addr);

    // SAFETY: the trie is laid out as C lays it out and stays frozen for the
    // test.
    let (view, lpm) = unsafe { (View::new(arena.root()), &*arena.root().ptr::<Lpm4>(addr)) };
    assert_eq!(
        [Some(5), Some(9), Some(11), Some(9), None, None],
        [
            [10, 0, 7, 7],
            [10, 1, 2, 255],
            [10, 1, 3, 4],
            [10, 1, 4, 0],
            [11, 0, 0, 0],
            [10, 2, 0, 0]
        ]
        .map(|key| lpm.lookup(&view, &key))
    );
}

/// Lays an IPv6 trie at `lpm` out as the C build lays it out: below the
/// first byte `0x20` a wide bucket maps `2001:db8::/64` to 8 and the rest to
/// 7; every other key maps to the invalid value.
fn lay_ipv6(arena: &'static BlockArena, lpm: usize) {
    let nodes = arena.alloc(2 * NODE).as_ptr().addr();
    let (root, bucket) = (nodes, nodes + NODE);
    fill(arena, root, u32::MAX);
    write(arena, root + 0x20 * 4, link(root, bucket, bindings::FAST_LPM_KIND_WIDE));
    for (idx, value) in [7, 8, 7].into_iter().enumerate() {
        write(arena, bucket + 4 * idx, leaf(value));
    }
    let bounds = [(0x2001_0db8_0000_0000u64, 0u64), (0x2001_0db8_0000_0001, 0)];
    for (idx, (high, low)) in bounds.into_iter().enumerate() {
        let at = bucket + bindings::FAST_LPM_WIDE_BOUNDS_OFFSET as usize + 16 * idx;
        write(arena, at, high);
        write(arena, at + 8, low);
    }
    set_root(arena, lpm, root);
}

/// The IPv6 address of a high and a low word.
fn ipv6(high: u64, low: u64) -> [u8; 16] {
    let mut key = [0; 16];
    key[..8].copy_from_slice(&high.to_be_bytes());
    key[8..].copy_from_slice(&low.to_be_bytes());
    key
}

/// verifies that the lookup compares a key with the bounds of a wide bucket
/// across its high and low words.
#[test]
fn test_lpm_lookup_compares_wide_bucket_bounds() {
    let arena = BlockArena::new();
    let addr = arena.alloc(size_of::<Lpm6>()).as_ptr().addr();
    lay_ipv6(arena, addr);

    // SAFETY: as above.
    let (view, lpm) = unsafe { (View::new(arena.root()), &*arena.root().ptr::<Lpm6>(addr)) };
    assert_eq!(
        [Some(7), Some(8), Some(8), Some(7), None],
        [
            ipv6(0x2001_0db7_ffff_ffff, u64::MAX),
            ipv6(0x2001_0db8_0000_0000, 0),
            ipv6(0x2001_0db8_0000_0000, u64::MAX),
            ipv6(0x2001_0db8_0000_0001, 0),
            ipv6(0x3000_0000_0000_0000, 0),
        ]
        .map(|key| lpm.lookup(&view, &key))
    );
}
