use core::net::Ipv6Addr;

use criterion::{Criterion, criterion_group, criterion_main};
use netip::{Ipv4Network, Ipv6Network};

fn bench_net_addrs(c: &mut Criterion) {
    let mut group = c.benchmark_group("netip");

    group.bench_function("Ipv4Network::addrs /24", |b| {
        let net = Ipv4Network::parse("77.88.55.0/24").unwrap();
        b.iter(|| {
            for addr in net.addrs() {
                core::hint::black_box(addr);
            }
        });
    });

    group.bench_function("Ipv4Network::addrs /16", |b| {
        let net = Ipv4Network::parse("77.88.0.0/16").unwrap();
        b.iter(|| {
            for addr in net.addrs() {
                core::hint::black_box(addr);
            }
        });
    });

    group.bench_function("Ipv4Network::addrs non-contiguous 8 host bits", |b| {
        let net = Ipv4Network::parse("192.168.0.1/255.255.0.255").unwrap();
        b.iter(|| {
            for addr in net.addrs() {
                core::hint::black_box(addr);
            }
        });
    });

    group.bench_function("Ipv6Network::addrs /120", |b| {
        let net = Ipv6Network::parse("2a02:6b8:c00::1234:0:0/120").unwrap();
        b.iter(|| {
            for addr in net.addrs() {
                core::hint::black_box(addr);
            }
        });
    });

    group.bench_function("Ipv6Network::addrs /112", |b| {
        let net = Ipv6Network::parse("2a02:6b8:c00::1234:0:0/112").unwrap();
        b.iter(|| {
            for addr in net.addrs() {
                core::hint::black_box(addr);
            }
        });
    });

    group.bench_function("Ipv6Network::addrs non-contiguous 8 host bits", |b| {
        let net = Ipv6Network::new(
            Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0x1234, 0, 0),
            Ipv6Addr::new(0xffff, 0xffff, 0xff00, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff),
        );
        b.iter(|| {
            for addr in net.addrs() {
                core::hint::black_box(addr);
            }
        });
    });

    group.finish();
}

fn bench_net_addrs_count(c: &mut Criterion) {
    let mut group = c.benchmark_group("netip");

    group.bench_function("Ipv4Network::addrs count /24", |b| {
        let net = Ipv4Network::parse("77.88.55.0/24").unwrap();
        b.iter(|| {
            core::hint::black_box(net.addrs().count());
        });
    });
}

criterion_group!(benches, bench_net_addrs, bench_net_addrs_count);
criterion_main!(benches);
