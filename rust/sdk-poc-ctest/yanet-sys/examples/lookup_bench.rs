//! Lookup throughput of the Rust port against the inlined C `lpm_lookup`.
//!
//! Both sides walk the same C-built tree with the same keys, in a tight loop
//! that stores every result; run with `cargo run --release --example
//! lookup_bench`. A rough single-core figure, not a dataplane benchmark.

use std::time::Instant;

use yanet_sys::{ConfigView, Lpm, LpmView, Root};
use yanet_testkit::{CImage, Family, XorShift, key_len, random_prefix};
use zerocopy::{FromBytes, KnownLayout};

/// Body of the C test image: an IPv4 and an IPv6 tree after the header.
#[derive(FromBytes, KnownLayout)]
#[repr(C)]
struct TwoTrees {
    v4: Lpm,
    v6: Lpm,
}

const KEYS: usize = 1 << 20;
const ROUNDS: usize = 20;

fn main() {
    let mut rng = XorShift::new(0xbe9c_0001);
    let mut image = CImage::new(256 << 20);
    for (family, count, lens) in [(Family::V4, 50_000, 8..=32), (Family::V6, 20_000, 16..=64)] {
        for _ in 0..count {
            let (from, to) = random_prefix(&mut rng, key_len(family), lens.clone());
            image.insert(family, &from, &to, rng.below(1 << 30) as u32);
        }
    }
    // SAFETY: the C image holds a C-built configuration of this layout and
    // is not modified while the view is used.
    let config = unsafe { ConfigView::<TwoTrees>::attach(Root::from_raw(image.config_ptr())) };

    for family in [Family::V4, Family::V6] {
        let size = key_len(family);
        let mut keys = vec![0u8; KEYS * size];
        rng.fill(&mut keys);
        let mut c_results = vec![0u32; KEYS];
        let mut rust_results = vec![0u32; KEYS];
        let view: LpmView<'_> = match family {
            Family::V4 => config.lpm(&config.body().v4),
            Family::V6 => config.lpm(&config.body().v6),
        };

        // Two passes, so that neither side only ever runs on a cold cache.
        for pass in 0..2 {
            let started = Instant::now();
            for _ in 0..ROUNDS {
                image.lookup_many(family, &keys, &mut c_results);
                core::hint::black_box(&mut c_results);
            }
            let c_ns = started.elapsed().as_nanos() as f64 / (ROUNDS * KEYS) as f64;

            let started = Instant::now();
            for _ in 0..ROUNDS {
                match family {
                    Family::V4 => lookup_all::<4>(&view, &keys, &mut rust_results),
                    Family::V6 => lookup_all::<16>(&view, &keys, &mut rust_results),
                }
                core::hint::black_box(&mut rust_results);
            }
            let rust_ns = started.elapsed().as_nanos() as f64 / (ROUNDS * KEYS) as f64;
            assert_eq!(c_results, rust_results);
            println!("{family:?} pass {pass}: C {c_ns:.2} ns/lookup, Rust {rust_ns:.2} ns/lookup");
        }
    }
}

#[inline(never)]
fn lookup_all<const N: usize>(view: &LpmView<'_>, keys: &[u8], results: &mut [u32]) {
    for (key, result) in keys.as_chunks::<N>().0.iter().zip(results) {
        *result = view.lookup(key);
    }
}
