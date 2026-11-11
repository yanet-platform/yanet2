//! The cost of a lookup in a hash index: a hit and a miss, over a few hot keys
//! and over the whole index, at 16 000 and 128 000 keys of each address
//! family.
//!
//! The keys are placed in their slots as the build of a configuration places
//! them, and every iteration looks one key up as a handler does. The keys of
//! the iterations come in turn from a ring drawn at random from the keys, so
//! a figure is the time of one lookup.

use core::{
    hint::black_box,
    net::{Ipv4Addr, Ipv6Addr},
    time::Duration,
};

use criterion::{BenchmarkId, Criterion, criterion_group, criterion_main};
use yanet_sdk_testing as _;
use yanet_shm::hash_index::{HashIndexKey, HashIndexRef, Slot, place, slots_for};

/// The sizes of the indexes measured.
const SIZES: [u32; 2] = [16_000, 128_000];

/// Keys a hot ring cycles over.
const HOT: usize = 64;

/// Keys a ring over the whole index cycles over, more than an index holds.
const WHOLE: usize = 1 << 18;

/// The `SplitMix64` generator: a fixed seed draws the same rings every run.
struct SplitMix(u64);

impl SplitMix {
    fn next(&mut self) -> u64 {
        self.0 = self.0.wrapping_add(0x9e37_79b9_7f4a_7c15);
        let mut z = self.0;
        z = (z ^ (z >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
        z ^ (z >> 31)
    }

    /// A ring of the given length of keys drawn from the first `count` keys.
    fn ring<K>(&mut self, len: usize, count: u32, key: impl Fn(u32) -> K) -> Vec<K> {
        (0..len).map(|_| key((self.next() % u64::from(count)) as u32)).collect()
    }
}

/// The IPv4 key of the given number: consecutive addresses of one network,
/// whose low bits alone differ. A miss takes them from another network.
fn ipv4(network: u32, idx: u32) -> Ipv4Addr {
    Ipv4Addr::from_bits(network << 24 | idx)
}

/// The IPv6 key of the given number, consecutive in one network as well.
fn ipv6(network: u32, idx: u32) -> Ipv6Addr {
    Ipv6Addr::from_bits(u128::from(network) << 96 | u128::from(idx))
}

/// The slots of an index of the first `count` keys of network 10, each naming
/// its own number.
fn slots<K: HashIndexKey>(count: u32, key: fn(u32, u32) -> K) -> Vec<Slot<K>> {
    let records: Vec<_> = (0..count).map(|idx| (key(10, idx), idx)).collect();
    let mut slots = vec![Slot::FREE; slots_for(records.len()).expect("a count that fits")];
    place(&mut slots, &records).expect("distinct keys");
    slots
}

/// Measures the lookups of one family at one size: a hit and a miss, over hot
/// keys and over the whole index.
fn lookups<K: HashIndexKey>(c: &mut Criterion, family: &str, count: u32, key: fn(u32, u32) -> K) {
    let slots = slots(count, key);
    let index = HashIndexRef::new(&slots);
    let mut rng = SplitMix(u64::from(count));
    let mut group = c.benchmark_group(family);
    for (outcome, network) in [("hit", 10), ("miss", 11)] {
        for (keys, len) in [("hot", HOT), ("whole", WHOLE)] {
            let ring = rng.ring(len, count, |idx| key(network, idx));
            group.bench_function(BenchmarkId::new(format!("{outcome}/{keys}"), count), |b| {
                let mut at = 0;
                b.iter(|| {
                    at = (at + 1) & (ring.len() - 1);
                    index.get(black_box(ring[at]))
                });
            });
        }
    }
    group.finish();
}

fn hash_index(c: &mut Criterion) {
    for count in SIZES {
        lookups(c, "ipv4", count, ipv4);
        lookups(c, "ipv6", count, ipv6);
    }
}

/// Short rounds, for a shared host: half a second of warm-up and two seconds
/// of measurement per figure.
fn config() -> Criterion {
    Criterion::default()
        .warm_up_time(Duration::from_millis(500))
        .measurement_time(Duration::from_secs(2))
        .sample_size(50)
}

criterion_group! {
    name = benches;
    config = config();
    targets = hash_index
}
criterion_main!(benches);
