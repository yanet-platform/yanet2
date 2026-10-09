//! Throughput of the Rust LPM lookup against the inline C lookup on the
//! same C-built LPM and keys.

use std::time::Instant;

use yanet_sdk::lpm::lookup;
use yanet_sys::testing::{CLpm, TestArena};

fn xorshift(state: &mut u64) -> u64 {
    *state ^= *state << 13;
    *state ^= *state >> 7;
    *state ^= *state << 17;
    *state
}

/// Rust lookups of consecutive `K`-byte keys.
fn rust_loop<const K: usize>(lpm: &CLpm<'_>, keys: &[u8], results: &mut [u32]) {
    let view = lpm.view::<K>();
    for (key, result) in keys.as_chunks::<K>().0.iter().zip(results.iter_mut()) {
        *result = lookup(view, key);
    }
}

fn main() {
    const KEYS: usize = 1 << 20;
    const ROUNDS: usize = 20;
    for (key_size, prefixes, arena) in [(4usize, 4000usize, 32usize << 20), (16, 1000, 128 << 20)] {
        let mut state = 0x0bad_5eed_u64 + key_size as u64;
        let arena = TestArena::new(arena);
        let mut lpm = arena.new_lpm();
        let mut starts = Vec::new();
        for value in 0..prefixes {
            let mut from: Vec<u8> = (0..key_size).map(|_| xorshift(&mut state) as u8).collect();
            let len = if key_size == 4 {
                8 + xorshift(&mut state) % 17
            } else {
                16 + xorshift(&mut state) % 49
            };
            let mut to = from.clone();
            for bit in len as usize..key_size * 8 {
                from[bit / 8] &= !(0x80 >> (bit % 8));
                to[bit / 8] |= 0x80 >> (bit % 8);
            }
            lpm.insert(&from, &to, value as u32 + 1);
            starts.push(from);
        }
        // Half of the keys fall inside inserted prefixes.
        let keys: Vec<u8> = (0..KEYS)
            .flat_map(|idx| {
                let base = &starts[xorshift(&mut state) as usize % starts.len()];
                let noise = xorshift(&mut state).to_be_bytes();
                (0..key_size)
                    .map(|b| {
                        if idx % 2 == 0 && b < key_size / 2 {
                            base[b]
                        } else {
                            noise[b % 8] ^ base[b]
                        }
                    })
                    .collect::<Vec<_>>()
            })
            .collect();
        let mut c_results = vec![0u32; KEYS];
        let mut best_c = f64::MAX;
        for _ in 0..ROUNDS {
            let start = Instant::now();
            lpm.lookup_many(key_size, &keys, &mut c_results);
            best_c = best_c.min(start.elapsed().as_secs_f64());
        }
        let mut rust_results = vec![0u32; KEYS];
        let mut best_rust = f64::MAX;
        for _ in 0..ROUNDS {
            let start = Instant::now();
            match key_size {
                4 => rust_loop::<4>(&lpm, &keys, &mut rust_results),
                _ => rust_loop::<16>(&lpm, &keys, &mut rust_results),
            }
            best_rust = best_rust.min(start.elapsed().as_secs_f64());
        }
        assert_eq!(c_results, rust_results);
        let ns = |secs: f64| secs * 1e9 / KEYS as f64;
        println!(
            "key size {key_size:>2}: C {:.2} ns/lookup, Rust {:.2} ns/lookup (best of {ROUNDS} x {KEYS} keys)",
            ns(best_c),
            ns(best_rust)
        );
    }
}

// The derive is a dependency of the library, visible to every example.
use trybuild as _;
use yanet_sdk_derive as _;
