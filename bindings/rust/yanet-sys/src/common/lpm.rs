//! The C LPM as a shared-memory field type.
//!
//! [`Lpm`] mirrors `struct fast_lpm` with its embedded memory context opaque,
//! so a reference to it stays sound while C rewrites the context's sibling
//! link. The lookup is a Rust port of the C one, over a trie the C routines
//! of `common/` build: the configuration builder embeds one by calling them.

use core::mem::{align_of, offset_of, size_of};

use yanet_shm::{
    opaque::Opaque,
    rel::RelPtr,
    resolve::Resolve,
    shm::{MAX_ALIGN, ShmLayout},
};

use crate::{bindings, cp::FfiRecord};

/// One full trie node: an entry per value of a key byte.
///
/// Only names the target of the root pointer; the lookup reads nodes and
/// buckets as words at their addresses.
type Node = [u32; 256];

/// An inclusive range of keys of `K` big-endian bytes and the value it maps
/// to, as the C insert takes them.
///
/// The value is below the invalid value, `0x7fffffff`, and the first key
/// does not exceed the last: a build refuses any other range. Ranges are
/// inserted in slice order, and on an overlap the later range wins, so a
/// broad range goes before the narrow ones in it.
///
/// The ranges are borrowed for the whole build, so they are made before it
/// starts, for a table in a box or a slice too. A range is also a record of an
/// api crate: the ranges C hands in are borrowed by the build as they are.
#[derive(Clone, Copy, Debug)]
#[repr(C)]
pub struct LpmRange<const K: usize> {
    /// First key of the range.
    pub from: [u8; K],
    /// Last key of the range.
    pub to: [u8; K],
    /// Value every key of the range maps to.
    pub value: u32,
}

impl<const K: usize> LpmRange<K> {
    /// The range of the leading bits of an address, mapped to a value.
    ///
    /// The bits of the address past the prefix length are ignored.
    ///
    /// ```
    /// use yanet_sys::common::lpm::LpmRange;
    ///
    /// let range = LpmRange::prefix([10, 1, 2, 3], 12, 7);
    /// assert_eq!(([10, 0, 0, 0], [10, 15, 255, 255]), (range.from, range.to));
    /// ```
    ///
    /// # Panics
    ///
    /// The prefix length is more than the bits of a key.
    #[must_use]
    pub fn prefix(addr: [u8; K], len: usize, value: u32) -> Self {
        assert!(len <= 8 * K, "a prefix of {len} bits in a key of {K} bytes");
        let mut from = addr;
        let mut to = addr;
        for (idx, (from, to)) in from.iter_mut().zip(&mut to).enumerate() {
            let kept = len.saturating_sub(idx * 8).min(8);
            let host = (0xffu16 >> kept) as u8;
            *from &= !host;
            *to |= host;
        }
        Self { from, to, value }
    }
}

// SAFETY: `#[repr(C)]` over byte arrays and an integer, without drop glue;
// every bit pattern is a range, a reversed one or an invalid value refused by
// the build.
unsafe impl<const K: usize> FfiRecord for LpmRange<K> {}

/// Longest-prefix-match table over big-endian keys of `K` bytes: 4, 8, 12
/// or 16.
///
/// A table built from no ranges is empty: it holds no node, opens no C memory
/// context and finds nothing, so it costs only its own bytes.
#[repr(C)]
pub struct Lpm<const K: usize> {
    /// The allocator context of the table's blocks. C rewrites its sibling
    /// link after publish when another context under the same parent is
    /// finalised.
    _memory_context: Opaque<bindings::memory_context>,
    /// The full root node.
    root: RelPtr<Node>,
    _chunks: RelPtr<u8>,
    _staged_from: RelPtr<u8>,
    _staged_to: RelPtr<u8>,
    _staged_value: RelPtr<u32>,
    _staged_count: u64,
    _staged_capacity: u64,
    _key_size: u8,
    /// C's `bool`, read by no Rust code.
    _frozen: u8,
}

const _: () = {
    type C = bindings::fast_lpm;
    type Rust = Lpm<4>;
    assert!(size_of::<Rust>() == size_of::<C>());
    assert!(align_of::<Rust>() == align_of::<C>());
    assert!(offset_of!(Rust, _memory_context) == offset_of!(C, memory_context));
    assert!(offset_of!(Rust, root) == offset_of!(C, root));
    assert!(offset_of!(Rust, _chunks) == offset_of!(C, chunks));
    assert!(offset_of!(Rust, _staged_from) == offset_of!(C, staged_from));
    assert!(offset_of!(Rust, _staged_to) == offset_of!(C, staged_to));
    assert!(offset_of!(Rust, _staged_value) == offset_of!(C, staged_value));
    assert!(offset_of!(Rust, _staged_count) == offset_of!(C, staged_count));
    assert!(offset_of!(Rust, _staged_capacity) == offset_of!(C, staged_capacity));
    assert!(offset_of!(Rust, _key_size) == offset_of!(C, key_size));
    assert!(offset_of!(Rust, _frozen) == offset_of!(C, frozen));
    assert!(align_of::<Rust>() <= MAX_ALIGN);
    assert!(size_of::<bool>() == size_of::<u8>());
};

// The mirrored fields keep the C types, not only their places.
#[allow(clippy::type_complexity)]
const _: fn(
    &bindings::fast_lpm,
) -> (
    &bindings::memory_context,
    *mut u8,
    *mut bindings::fast_lpm_chunk,
    *mut u8,
    *mut u8,
    *mut u32,
    u64,
    u64,
    u8,
    bool,
) = |lpm| {
    (
        &lpm.memory_context,
        lpm.root,
        lpm.chunks,
        lpm.staged_from,
        lpm.staged_to,
        lpm.staged_value,
        lpm.staged_count,
        lpm.staged_capacity,
        lpm.key_size,
        lpm.frozen,
    )
};

/// The value of a key no range covers.
const INVALID: u32 = bindings::FAST_LPM_VALUE_INVALID;
/// An entry with this bit is a leaf: the value shifted left by one.
const LEAF: u32 = bindings::FAST_LPM_ENTRY_LEAF;
/// The child kind bits of a link entry.
const KIND: u32 = bindings::FAST_LPM_ENTRY_KIND_MASK;
const KIND_FULL: u32 = bindings::FAST_LPM_KIND_FULL;
const KIND_NARROW: u32 = bindings::FAST_LPM_KIND_NARROW;
/// The bytes of a link's distance unit, 64, per unit of its field.
const LINK_SCALE: isize = bindings::FAST_LPM_NODE_ALIGN as isize / 8;
const NARROW_BOUNDS: usize = bindings::FAST_LPM_NARROW_BOUNDS as usize;
const NARROW_BOUNDS_OFFSET: usize = bindings::FAST_LPM_NARROW_BOUNDS_OFFSET as usize;
const WIDE_BOUNDS_OFFSET: usize = bindings::FAST_LPM_WIDE_BOUNDS_OFFSET as usize;

/// The word at a byte distance from a node of the trie.
///
/// # Safety
///
/// The word lies inside a node or bucket of a trie frozen for the caller's
/// lifetime, aligned for its type.
#[inline(always)]
unsafe fn read<T: Copy>(node: *const u8, offset: usize) -> T {
    // SAFETY: as the caller vouches.
    unsafe { node.add(offset).cast::<T>().read() }
}

impl<const K: usize> Lpm<K> {
    /// Reports whether the table was built from no ranges, so it holds no node.
    #[inline(always)]
    pub fn is_empty(&self) -> bool {
        self.root.offset() == 0
    }

    /// Looks a big-endian key up through `cx`, bit for bit like the C
    /// `fast_lpm_lookup`; `None` when no inserted range covers it.
    ///
    /// Walks one full node per key byte from the root until a leaf entry or a
    /// link to a bucket, whose bounds the rest of the key is compared with.
    #[doc(alias = "fast_lpm_lookup")]
    #[inline(always)]
    pub fn lookup<'g>(&'g self, cx: &impl Resolve<'g>, key: &[u8; K]) -> Option<u32> {
        const {
            assert!(
                K == 4 || K == 8 || K == 12 || K == 16,
                "LPM keys are 4, 8, 12 or 16 bytes"
            )
        };
        if self.is_empty() {
            return None;
        }
        let root = cx.shm_root();
        let root_slot = core::ptr::from_ref(&self.root).addr();
        let mut node: *const u8 = root.rel(root_slot, self.root.offset());
        let mut entry = u32::MAX;
        for &byte in key {
            // SAFETY: `node` is a full node of this trie (C built the links)
            // of 256 aligned entries.
            entry = unsafe { read::<u32>(node, 4 * usize::from(byte)) };
            if entry & LEAF != 0 {
                break;
            }
            let distance = (entry & !(LEAF | KIND)) as i32 as isize * LINK_SCALE;
            node = root.rel(node.addr(), distance);
            if entry & KIND != KIND_FULL {
                // SAFETY: the link names a bucket of the kind its bits give.
                entry = unsafe {
                    if entry & KIND == KIND_NARROW || K <= 8 {
                        Self::narrow(node, key)
                    } else {
                        Self::wide(node, key)
                    }
                };
                break;
            }
        }
        let value = entry >> 1;
        (value != INVALID).then_some(value)
    }

    /// The leaf of a key in a bucket of four bounds over the last bytes of
    /// the key, stored with the sign bit flipped, and five values in reverse
    /// order: the count of bounds above the key picks the value.
    ///
    /// # Safety
    ///
    /// `node` is a narrow bucket of a trie frozen for the caller's lifetime.
    #[inline(always)]
    unsafe fn narrow(node: *const u8, key: &[u8; K]) -> u32 {
        let above = if K == 4 {
            let key = (u32::from_be_bytes([key[0], key[1], key[2], key[3]]) ^ 0x8000_0000) as i32;
            (0..NARROW_BOUNDS)
                // SAFETY: the bounds lie in the bucket (caller contract).
                .filter(|&idx| unsafe { read::<i32>(node, NARROW_BOUNDS_OFFSET + 4 * idx) } > key)
                .count()
        } else {
            let mut low = [0; 8];
            low.copy_from_slice(&key[K - 8..]);
            let key = (u64::from_be_bytes(low) ^ 0x8000_0000_0000_0000) as i64;
            (0..NARROW_BOUNDS)
                // SAFETY: as above.
                .filter(|&idx| unsafe { read::<i64>(node, NARROW_BOUNDS_OFFSET + 8 * idx) } > key)
                .count()
        };
        // SAFETY: as above, the values lead the bucket.
        unsafe { read::<u32>(node, 4 * above) }
    }

    /// The leaf of a key in a bucket of two bounds over the whole key, as
    /// high and low words, and three values: the count of bounds at or below
    /// the key picks the value.
    ///
    /// # Safety
    ///
    /// `node` is a wide bucket of a trie frozen for the caller's lifetime;
    /// only keys longer than 8 bytes have one.
    #[inline(always)]
    unsafe fn wide(node: *const u8, key: &[u8; K]) -> u32 {
        let mut high = [0; 8];
        high[16 - K..].copy_from_slice(&key[..K - 8]);
        let high = u64::from_be_bytes(high);
        let mut low = [0; 8];
        low.copy_from_slice(&key[K - 8..]);
        let low = u64::from_be_bytes(low);
        let below = (0..2)
            .filter(|&idx| {
                // SAFETY: the bounds lie in the bucket (caller contract).
                let (bound_high, bound_low) = unsafe {
                    (
                        read::<u64>(node, WIDE_BOUNDS_OFFSET + 16 * idx),
                        read::<u64>(node, WIDE_BOUNDS_OFFSET + 16 * idx + 8),
                    )
                };
                bound_high < high || (bound_high == high && bound_low <= low)
            })
            .count();
        // SAFETY: as above, the values lead the bucket.
        unsafe { read::<u32>(node, 4 * below) }
    }
}

/// IPv4 LPM.
pub type Lpm4 = Lpm<4>;
/// IPv6 LPM.
pub type Lpm6 = Lpm<16>;

// SAFETY: `Lpm` is a `#[repr(C)]` mirror of the C struct whose size and
// field offsets are asserted equal to the bindings'. Every pattern is valid:
// the opaque context is never read by Rust, the pointer slots are offset
// words, the counts integers and C's `bool` a byte no Rust code reads.
// Dropping is a no-op and the alignment is 8.
unsafe impl<const K: usize> ShmLayout for Lpm<K> {}
