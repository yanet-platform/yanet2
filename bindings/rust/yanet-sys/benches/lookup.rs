//! Classifier lookups over prebuilt trees, with oracle checks outside timing.

extern crate alloc;

#[path = "../tests/support/arena.rs"]
mod arena;
#[path = "../tests/support/classify.rs"]
mod classify;

use core::{hint::black_box, time::Duration};

use arena::ArenaOwner;
use classify::{TestRule, any_or, build_any, build4, build6, edges, first, in4, in6, net4, net6};
use criterion::{Criterion, Throughput, criterion_group, criterion_main};
use yanet_sys::classify::{LineRange, Net4, Net6, PortRange, Rule};

/// Eight source networks with shared transport ranges and no fallback.
fn rules4() -> Vec<TestRule<Net4>> {
    (0..8)
        .map(|index| TestRule {
            src: vec![net4([10, index, 0, 0], 16)],
            dst: vec![net4([172, 16, 0, 0], 12)],
            protos: vec![LineRange { from: 6, to: 17 }],
            ports: vec![PortRange {
                from: 1000,
                to: 2000 + u16::from(index) * 100,
            }],
        })
        .collect()
}

/// Overlapping high halves whose constrained low halves require combination
/// rows.
fn rules6() -> Vec<TestRule<Net6>> {
    (0..8)
        .map(|index| TestRule {
            src: vec![net6((0x2001_0db8_0000_0000u128 << 64) | (index << 32), 96)],
            dst: vec![net6(0x2001_0db9_0000_0000u128 << 64, 64)],
            protos: vec![LineRange { from: 6, to: 17 }],
            ports: vec![PortRange {
                from: 1000,
                to: 2000 + index as u16 * 100,
            }],
        })
        .collect()
}

/// Measures the same key batch in scalar and burst form after checking both.
fn measure<K: Copy>(
    criterion: &mut Criterion,
    name: &str,
    keys: &[K],
    expected: &[Option<usize>],
    lookup: impl Fn(K) -> Rule,
    burst: impl Fn(&[K], &mut [Rule]),
) {
    let single = keys.iter().map(|&key| lookup(key).index()).collect::<Vec<_>>();
    assert_eq!(expected, single);
    let mut output = vec![Rule::NONE; keys.len()];
    burst(keys, &mut output);
    let actual = output.iter().map(|rule| rule.index()).collect::<Vec<_>>();
    assert_eq!(expected, actual);

    let mut group = criterion.benchmark_group(name);
    group.throughput(Throughput::Elements(keys.len() as u64));
    group.bench_function("scalar", |bench| {
        bench.iter(|| {
            for &key in black_box(keys) {
                black_box(lookup(black_box(key)));
            }
        });
    });
    group.bench_function("burst", |bench| {
        bench.iter(|| {
            burst(black_box(keys), black_box(&mut output));
            black_box(&output);
        });
    });
    group.finish();
}

fn lookup(criterion: &mut Criterion) {
    let owner = ArenaOwner::new();
    let free = owner.free_bytes();
    let rules4 = rules4();
    let rules6 = rules6();
    let built4 = build4(&owner, &rules4);
    let built6 = build6(&owner, &rules6);
    let projection = (0..rules4.len()).map(|index| index % 2 == 1).collect::<Vec<_>>();
    let decoded = build_any(&owner, &rules4, &projection);
    let tree4 = owner.body(&built4).get(&owner);
    let tree6 = owner.body(&built6).get(&owner);
    let decode = owner.body(&decoded).get(&owner);
    let (protos, ports) = edges(&rules4);
    let transport = (0..32)
        .map(|index| (protos[index % protos.len()], ports[index % ports.len()]))
        .collect::<Vec<_>>();
    let keys4 = transport
        .iter()
        .enumerate()
        .map(|(index, &(proto, port))| {
            let src = [10, (index % 10) as u8, 0, 1];
            let dst = [172, 17, 0, 1];
            ((src, dst), (proto, port))
        })
        .collect::<Vec<_>>();
    let expected4 = transport
        .iter()
        .enumerate()
        .map(|(index, &(proto, port))| {
            let src = u32::from_be_bytes([10, (index % 10) as u8, 0, 1]);
            let dst = u32::from_be_bytes([172, 17, 0, 1]);
            first(
                &rules4,
                |rule| any_or(&rule.src, |net| in4(net, src)) && any_or(&rule.dst, |net| in4(net, dst)),
                proto,
                port,
            )
        })
        .collect::<Vec<_>>();
    let keys6 = transport
        .iter()
        .enumerate()
        .map(|(index, &(proto, port))| {
            let src = (0x2001_0db8_0000_0000u128 << 64) | (((index % 10) as u128) << 32) | 1;
            let dst = (0x2001_0db9_0000_0000u128 << 64) | 1;
            ((src.to_be_bytes(), dst.to_be_bytes()), (proto, port))
        })
        .collect::<Vec<_>>();
    let expected6 = keys6
        .iter()
        .map(|&((src, dst), (proto, port))| {
            first(
                &rules6,
                |rule| {
                    any_or(&rule.src, |net| in6(net, u128::from_be_bytes(src)))
                        && any_or(&rule.dst, |net| in6(net, u128::from_be_bytes(dst)))
                },
                proto,
                port,
            )
        })
        .collect::<Vec<_>>();
    let expected_decode = transport
        .iter()
        .map(|&(proto, port)| {
            rules4.iter().enumerate().find_map(|(index, rule)| {
                (projection[index]
                    && any_or(&rule.protos, |range| {
                        (range.from..=range.to).contains(&u32::from(proto))
                    })
                    && any_or(&rule.ports, |range| (range.from..=range.to).contains(&port)))
                .then_some(index)
            })
        })
        .collect::<Vec<_>>();

    measure(
        criterion,
        "rules4",
        &keys4,
        &expected4,
        |key| tree4.lookup(&owner, key),
        |keys, output| tree4.lookup_burst(&owner, keys, |&key| key, output),
    );
    measure(
        criterion,
        "rules6",
        &keys6,
        &expected6,
        |key| tree6.lookup(&owner, key),
        |keys, output| tree6.lookup_burst(&owner, keys, |&key| key, output),
    );
    measure(
        criterion,
        "decode",
        &transport,
        &expected_decode,
        |key| decode.lookup(&owner, key),
        |keys, output| decode.lookup_burst(&owner, keys, |&key| key, output),
    );
    // SAFETY: each tree was built by this owner and no lookup remains live.
    unsafe { built4.destroy(&owner) };
    // SAFETY: as above, this tree has no remaining borrowers.
    unsafe { built6.destroy(&owner) };
    // SAFETY: as above, this decoding has no remaining borrowers.
    unsafe { decoded.destroy(&owner) };
    assert_eq!(free, owner.free_bytes());
}

criterion_group! {
    name = benches;
    config = Criterion::default().sample_size(100).warm_up_time(Duration::from_millis(500)).measurement_time(Duration::from_millis(1500));
    targets = lookup
}
criterion_main!(benches);
