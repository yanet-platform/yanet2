//! The slots of an exact-match index and their lookup.
//!
//! The index of a configuration names the position of a key in an array of
//! the author's, or tells that no position has it. The control plane places
//! the keys of records, each key at most once and every position below
//! `u32::MAX`, and the dataplane reads the slots through a [`HashIndexRef`]
//! resolved for a batch.
//!
//! The slots are open addressing with linear probing over a power of two of
//! them, at least half free, so that a walk ends at a free slot after a short
//! run. Unlike C `hash_index`, a slot holds the key itself, so a lookup reads
//! no other array.

use core::{
    mem::offset_of,
    net::{Ipv4Addr, Ipv6Addr},
};

use crate::{
    layout::{Field, Kind, Node},
    shm::ShmLayout,
};

/// The position of a slot that holds no key.
const FREE: u32 = u32::MAX;

/// A key of an exact-match index: a value of a fixed layout, compared whole.
///
/// Equal keys must give equal bits, as a hash and an equality must agree for a
/// map of the standard library. A key that breaks this makes an index that
/// misses keys, and nothing worse.
pub trait HashIndexKey: ShmLayout + Copy + Eq {
    /// The key a free slot holds.
    ///
    /// A free slot is told by its position, so an index may hold this key
    /// too.
    const UNSPECIFIED: Self;

    /// The bits of the key that pick its first slot.
    ///
    /// The index mixes the bits itself, so a key gives them as they are. The
    /// control plane places the key from the same bits.
    fn bits(self) -> u64;
}

impl HashIndexKey for Ipv4Addr {
    const UNSPECIFIED: Self = Ipv4Addr::UNSPECIFIED;

    fn bits(self) -> u64 {
        u64::from(self.to_bits())
    }
}

impl HashIndexKey for Ipv6Addr {
    const UNSPECIFIED: Self = Ipv6Addr::UNSPECIFIED;

    /// The two halves of the address folded into one.
    fn bits(self) -> u64 {
        let bits = self.to_bits();
        ((bits >> 64) ^ bits) as u64
    }
}

/// One slot of an index: a key and the position it names, or free.
#[doc(hidden)]
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(C)]
pub struct HashIndexSlot<K> {
    key: K,
    position: u32,
}

impl<K: HashIndexKey> HashIndexSlot<K> {
    /// A slot that holds no key.
    pub const FREE: Self = Self { key: K::UNSPECIFIED, position: FREE };

    /// A slot that holds the key and names the position.
    pub const fn new(key: K, position: u32) -> Self {
        Self { key, position }
    }
}

// SAFETY: a key of a fixed layout and a 32-bit word, both valid for every bit
// pattern and needing no drop, aligned as the larger of the two.
unsafe impl<K: ShmLayout> ShmLayout for HashIndexSlot<K> {
    const LAYOUT: &'static Node = &Node::of::<Self>(Kind::Struct {
        name: concat!(module_path!(), "::HashIndexSlot"),
        fields: &[
            Field {
                name: "key",
                offset: offset_of!(Self, key),
                ty: K::LAYOUT,
            },
            Field {
                name: "position",
                offset: offset_of!(Self, position),
                ty: u32::LAYOUT,
            },
        ],
    });
}

/// An exact-match index resolved for the batch of one handler call.
#[derive(Clone, Copy)]
pub struct HashIndexRef<'g, K> {
    slots: &'g [HashIndexSlot<K>],
}

impl<'g, K: HashIndexKey> HashIndexRef<'g, K> {
    /// The index over the slots that the control plane placed.
    #[doc(hidden)]
    pub fn new(slots: &'g [HashIndexSlot<K>]) -> Self {
        Self { slots }
    }

    /// The position the key names, when the index holds the key.
    #[inline]
    pub fn get(&self, key: K) -> Option<u32> {
        let HashIndexRef { slots } = *self;
        for at in probe(slots.len(), key) {
            let slot = slots.get(at)?;
            if slot.position == FREE {
                return None;
            }
            if slot.key == key {
                return Some(slot.position);
            }
        }
        None
    }
}

/// The slots that the walk of a key visits, in order: every slot of an index
/// of the given length once, from the key's home slot on.
fn probe<K: HashIndexKey>(len: usize, key: K) -> impl Iterator<Item = usize> {
    // Fibonacci hashing: the high bits of the product depend on every bit of
    // the key, so keys that differ only in their low bits, as the addresses
    // of one prefix do, spread over the whole index.
    let hash = key.bits().wrapping_mul(0x9e37_79b9_7f4a_7c15);
    let home = hash.checked_shr(u64::BITS - len.trailing_zeros()).unwrap_or(0) as usize;
    let mask = len.wrapping_sub(1);
    (0..len).map(move |step| home.wrapping_add(step) & mask)
}

#[cfg(test)]
mod tests {
    use core::net::Ipv4Addr;

    use super::{HashIndexKey, HashIndexRef, HashIndexSlot, probe};

    /// The first `N` addresses whose walks start at the home slot of the
    /// address 0.0.0.1 in an index of the given length.
    fn colliding<const N: usize>(len: usize) -> [Ipv4Addr; N] {
        let home = probe(len, Ipv4Addr::from_bits(1)).next();
        let mut keys = (1..)
            .map(Ipv4Addr::from_bits)
            .filter(|&key| probe(len, key).next() == home);
        core::array::from_fn(|_| keys.next().unwrap())
    }

    /// An index over the slots.
    fn index<K: HashIndexKey>(slots: &[HashIndexSlot<K>]) -> HashIndexRef<'_, K> {
        HashIndexRef::new(slots)
    }

    #[test]
    fn test_get_misses_the_unspecified_key_among_free_slots() {
        let slots = [HashIndexSlot::FREE; 4];

        assert_eq!(None, index(&slots).get(Ipv4Addr::UNSPECIFIED));
    }

    /// verifies that a walk passes over the slots of other keys: a key whose
    /// home slot another key took is found, and an absent key with that home
    /// slot is not.
    #[test]
    fn test_get_passes_over_the_slots_of_other_keys() {
        let mut slots = [HashIndexSlot::FREE; 8];
        let [first, second, absent] = colliding(slots.len());
        let home = probe(slots.len(), first).next().unwrap();
        slots[home] = HashIndexSlot { key: first, position: 0 };
        slots[(home + 1) % slots.len()] = HashIndexSlot { key: second, position: 1 };

        assert_eq!(Some(1), index(&slots).get(second));
        assert_eq!(None, index(&slots).get(absent));
    }

    /// verifies that a walk over slots without a free one ends, finding every
    /// key the slots hold and missing the others.
    #[test]
    fn test_get_ends_over_slots_without_a_free_one() {
        let slots: [HashIndexSlot<Ipv4Addr>; 4] = core::array::from_fn(|idx| HashIndexSlot {
            key: Ipv4Addr::from_bits(idx as u32),
            position: idx as u32,
        });

        for position in 0..4 {
            assert_eq!(Some(position), index(&slots).get(Ipv4Addr::from_bits(position)));
        }
        assert_eq!(None, index(&slots).get(Ipv4Addr::from_bits(4)));
    }
}
