//! The slots of an exact-match index, their placement and their lookup.
//!
//! The index of a configuration names the position of a key in an array of
//! the author's, or tells that no position has it. Its build places the keys
//! of records, each key at most once and every position below `u32::MAX`,
//! and the dataplane reads the slots through a [`HashIndexRef`] resolved for a
//! batch.
//!
//! The slots are open addressing with linear probing over a power of two of
//! them, at least half free, so that a walk ends at a free slot after a short
//! run. Unlike C `hash_index`, a slot holds the key itself, so a lookup reads
//! no other array.

use core::net::{Ipv4Addr, Ipv6Addr};

use crate::shm::ShmLayout;

/// The position of a slot that holds no key.
const FREE: u32 = u32::MAX;

/// A key of an exact-match index: a value of a fixed layout, compared whole.
///
/// Equal keys must give equal bits, as a hash and an equality must agree for a
/// map of the standard library. A key that breaks this makes an index that
/// misses keys and lets a key given twice through, and nothing worse. A key
/// whose code panics fails the build of an index, which then leaves nothing
/// allocated.
pub trait HashIndexKey: ShmLayout + Copy + Eq {
    /// The key a free slot holds.
    ///
    /// A free slot is told by its position, so an index may hold this key
    /// too.
    const UNSPECIFIED: Self;

    /// The bits of the key that pick its first slot.
    ///
    /// The index mixes the bits itself, so a key gives them as they are.
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
pub struct Slot<K> {
    key: K,
    position: u32,
}

impl<K: HashIndexKey> Slot<K> {
    /// A slot that holds no key.
    pub const FREE: Self = Self { key: K::UNSPECIFIED, position: FREE };
}

// SAFETY: a key of a fixed layout and a 32-bit word, both valid for every bit
// pattern and needing no drop, aligned as the larger of the two.
unsafe impl<K: ShmLayout> ShmLayout for Slot<K> {}

/// How many slots an index of the given number of records takes: a power of
/// two that leaves at least half of them free.
///
/// `None` when the count overflows.
#[doc(hidden)]
pub fn slots_for(records: usize) -> Option<usize> {
    records.checked_mul(2)?.checked_next_power_of_two()
}

/// An exact-match index resolved for the batch of one handler call.
#[derive(Clone, Copy)]
pub struct HashIndexRef<'g, K> {
    slots: &'g [Slot<K>],
}

impl<'g, K: HashIndexKey> HashIndexRef<'g, K> {
    /// The index over the slots that a build placed.
    #[doc(hidden)]
    pub fn new(slots: &'g [Slot<K>]) -> Self {
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

/// Why the records of a build do not make an index.
#[doc(hidden)]
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Unplaced {
    /// Two records give one key.
    DuplicateKey {
        /// The position of the record that holds the key.
        first: u32,
        /// The position of the record refused.
        second: u32,
    },
    /// A record names the position that marks a free slot.
    InvalidPosition,
    /// The iterator yields fewer or more records than its declared length.
    LengthMismatch,
}

/// Places the key of every record in a slot, in the order of the records.
///
/// The slots are free and number at least twice the records, as the count of
/// an index gives them, so the walk of every key meets a free slot. On a
/// refusal the slots hold the keys placed before it.
#[doc(hidden)]
pub fn place<K: HashIndexKey>(slots: &mut [Slot<K>], records: &[(K, u32)]) -> Result<(), Unplaced> {
    place_iter(slots, records.len(), records.iter().copied())
}

/// Places records in iterator order and checks their declared count.
///
/// The slots number at least twice the declared count. An empty input may
/// have no slots. A refusal leaves the records placed before it in the slots.
#[doc(hidden)]
pub fn place_iter<K: HashIndexKey>(
    slots: &mut [Slot<K>],
    len: usize,
    mut records: impl Iterator<Item = (K, u32)>,
) -> Result<(), Unplaced> {
    debug_assert!(len == 0 || slots.len() > len, "a free slot is left for every key");
    for _ in 0..len {
        let (key, position) = records.next().ok_or(Unplaced::LengthMismatch)?;
        if position == FREE {
            return Err(Unplaced::InvalidPosition);
        }
        for at in probe(slots.len(), key) {
            let Some(slot) = slots.get_mut(at) else {
                break;
            };
            if slot.position == FREE {
                *slot = Slot { key, position };
                break;
            }
            if slot.key == key {
                return Err(Unplaced::DuplicateKey {
                    first: slot.position,
                    second: position,
                });
            }
        }
    }
    if records.next().is_some() {
        return Err(Unplaced::LengthMismatch);
    }
    Ok(())
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

    use super::{HashIndexKey, HashIndexRef, Slot, place, probe, slots_for};

    /// The first `N` addresses whose walks start at the home slot of the
    /// address 0.0.0.1 in an index of the given length.
    fn colliding<const N: usize>(len: usize) -> [Ipv4Addr; N] {
        let home = probe(len, Ipv4Addr::from_bits(1)).next();
        let mut keys = (1..)
            .map(Ipv4Addr::from_bits)
            .filter(|&key| probe(len, key).next() == home);
        core::array::from_fn(|_| keys.next().unwrap())
    }

    /// An index over slots a placement filled.
    fn index<K: HashIndexKey>(slots: &[Slot<K>]) -> HashIndexRef<'_, K> {
        HashIndexRef::new(slots)
    }

    #[test]
    fn test_slots_for_leaves_half_of_the_slots_free() {
        assert_eq!(Some(2), slots_for(1));
        assert_eq!(Some(8), slots_for(3));
        assert_eq!(Some(262_144), slots_for(128_000));
        assert_eq!(None, slots_for(usize::MAX / 2 + 1));
    }

    #[test]
    fn test_get_misses_the_unspecified_key_among_free_slots() {
        let slots = [Slot::FREE; 4];

        assert_eq!(None, index(&slots).get(Ipv4Addr::UNSPECIFIED));
    }

    /// verifies that keys with one home slot are all kept and all found.
    #[test]
    fn test_place_keeps_keys_of_one_home_slot() {
        let mut slots = [Slot::FREE; 8];
        let [first, second, third] = colliding(slots.len());
        assert_eq!(Ok(()), place(&mut slots, &[(first, 0), (second, 1), (third, 2)]));

        assert_eq!(
            [Some(0), Some(1), Some(2)],
            [first, second, third].map(|key| index(&slots).get(key))
        );
    }

    /// verifies that a walk passes over the slots of other keys: a key whose
    /// home slot another key took is found, and an absent key with that home
    /// slot is not.
    #[test]
    fn test_get_passes_over_the_slots_of_other_keys() {
        let mut slots = [Slot::FREE; 8];
        let [first, second, absent] = colliding(slots.len());
        assert_eq!(Ok(()), place(&mut slots, &[(first, 0), (second, 1)]));

        assert_eq!(Some(1), index(&slots).get(second));
        assert_eq!(None, index(&slots).get(absent));
    }

    /// verifies that a walk over slots without a free one ends, finding every
    /// key the slots hold and missing the others.
    #[test]
    fn test_get_ends_over_slots_without_a_free_one() {
        let slots: [Slot<Ipv4Addr>; 4] = core::array::from_fn(|idx| Slot {
            key: Ipv4Addr::from_bits(idx as u32),
            position: idx as u32,
        });

        for position in 0..4 {
            assert_eq!(Some(position), index(&slots).get(Ipv4Addr::from_bits(position)));
        }
        assert_eq!(None, index(&slots).get(Ipv4Addr::from_bits(4)));
    }
}
