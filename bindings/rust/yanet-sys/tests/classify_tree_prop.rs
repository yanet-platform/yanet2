//! The classifier trees against the first rule each key matches, over random
//! rulesets the C compiler builds.
//!
//! The networks are drawn from small pools of addresses and lengths, so they
//! collide, nest and repeat; the IPv6 ones are contiguous in each 64-bit
//! half, as the IPv6 compile ranges the halves apart. Each lookup and burst
//! lookup is held to the oracle that matches the rules one by one, at every
//! edge of a network or range of the rules, and at random keys.
//! `PROPTEST_CASES` sets the number of rulesets. Natively only: Miri does not
//! run C.

extern crate alloc;

#[path = "support/arena.rs"]
mod arena;
#[path = "support/classify.rs"]
mod classify;

use core::{
    fmt::{self, Debug, Formatter},
    hash::{BuildHasher, Hasher},
    net::{Ipv4Addr, Ipv6Addr},
    panic::AssertUnwindSafe,
};
use std::{collections::hash_map::RandomState, env, panic};

use arena::ArenaOwner;
use classify::{TestRule, any_or, build4, build6, edges, first, in4, in6, net4, net6};
use proptest::{
    arbitrary::any,
    bool::weighted,
    collection::vec,
    prop_oneof,
    sample::select,
    strategy::{Just, Strategy},
    test_runner::{Config, RngSeed, TestCaseError, TestError, TestRunner},
};
use yanet_sys::classify::{Addr4, LineRange, Net4, Net6, PortRange, Rule};

/// Rulesets per property when `PROPTEST_CASES` is unset: a few seconds of a
/// debug build.
const CASES: u32 = 64;

/// The most rules of a ruleset.
const MAX_RULES: usize = 64;

/// The random protocol and port pairs an edge address meets beyond its edge
/// protocols and ports.
const PARTNERS: usize = 4;

/// The keys of a case drawn at random from the edges and the whole domains.
const RANDOM_KEYS: usize = 256;

/// The IPv4 addresses the networks are cut from: nested and adjacent.
const ADDRS4: [Ipv4Addr; 8] = [
    Ipv4Addr::new(10, 0, 0, 0),
    Ipv4Addr::new(10, 0, 0, 1),
    Ipv4Addr::new(10, 0, 0, 128),
    Ipv4Addr::new(10, 0, 1, 0),
    Ipv4Addr::new(10, 1, 0, 0),
    Ipv4Addr::new(10, 128, 0, 0),
    Ipv4Addr::new(192, 0, 2, 255),
    Ipv4Addr::BROADCAST,
];

const LENS4: [u32; 9] = [8, 16, 24, 32, 0, 1, 9, 25, 31];

/// The high halves of the IPv6 networks: one documentation prefix, its
/// neighbours, and the ends of the half.
const HIGHS6: [u64; 7] = [
    0x2001_0db8_0000_0000,
    0x2001_0db8_0000_0001,
    0x2001_0db8_0001_0000,
    0x2001_0db8_8000_0000,
    0x2001_0db9_0000_0000,
    0,
    u64::MAX,
];

/// The low halves of the IPv6 networks: hosts, the halves of the half, and
/// its ends.
const LOWS6: [u64; 6] = [0, 1, 1 << 63, (1 << 63) | 1, 1 << 32, u64::MAX];

/// The lengths of the IPv6 networks that are prefixes.
const PREFIXES6: [u32; 10] = [48, 64, 128, 0, 16, 32, 63, 65, 96, 127];

/// The lengths of each half of the IPv6 networks that are not prefixes: a
/// short high half over a long low one.
const HALF_LENS6: [u32; 6] = [0, 64, 1, 16, 48, 63];

/// The protocol bounds, the last two beyond the byte domain the compile
/// clamps them to.
const PROTOS: [u32; 8] = [6, 17, 1, 0, 58, 255, 256, u32::MAX];

const PORTS: [u16; 9] = [80, 443, 0, 1, 22, 53, 1024, 8080, u16::MAX];

/// An IPv4 network of the pools, its address mostly cut to its length,
/// else with its host bits left for the compile to drop.
fn net4s() -> impl Strategy<Value = Written<Net4>> + Clone {
    (select(&ADDRS4[..]), select(&LENS4[..]), weighted(0.25)).prop_map(|(addr, len, host_bits)| {
        let net = net4(addr.octets(), len);
        let addr = if host_bits {
            u32::from(addr)
        } else {
            u32::from(addr) & u32::from_be_bytes(net.mask)
        };
        Written(Net4 {
            addr: addr.to_be_bytes(),
            mask: net.mask,
        })
    })
}

/// An IPv6 network of the pools: mostly a prefix, else a mask contiguous in
/// each half; its address mostly cut to its mask, else with its host bits
/// left for the compile to drop.
fn net6s() -> impl Strategy<Value = Written<Net6>> + Clone {
    let lens = prop_oneof![
        2 => select(&PREFIXES6[..]).prop_map(|len| (len.min(64), len.saturating_sub(64))),
        1 => (select(&HALF_LENS6[..]), select(&HALF_LENS6[..])),
    ];
    let parts = (select(&HIGHS6[..]), select(&LOWS6[..]), lens, weighted(0.25));
    parts.prop_map(|(high, low, (high_len, low_len), host_bits)| {
        let addr = (u128::from(high) << 64) | u128::from(low);
        // Each half's mask is a prefix of its length within the half.
        let high_mask = u128::from_be_bytes(net6(0, high_len).mask);
        let low_mask = u128::from_be_bytes(net6(0, 64 + low_len).mask) & u128::from(u64::MAX);
        let mask = high_mask | low_mask;
        let addr = if host_bits { addr } else { addr & mask };
        Written(Net6 {
            addr: addr.to_be_bytes(),
            mask: mask.to_be_bytes(),
        })
    })
}

/// A protocol range of the pool: mostly one protocol.
fn proto_ranges() -> impl Strategy<Value = Written<LineRange>> {
    prop_oneof![
        2 => select(&PROTOS[..]).prop_map(|proto| Written(LineRange { from: proto, to: proto })),
        1 => (select(&PROTOS[..]), select(&PROTOS[..])).prop_map(|(lhs, rhs)| Written(LineRange {
            from: lhs.min(rhs),
            to: lhs.max(rhs),
        })),
    ]
}

/// A port range of the pool: one port, a range between two, or every port.
fn port_ranges() -> impl Strategy<Value = Written<PortRange>> {
    prop_oneof![
        3 => select(&PORTS[..]).prop_map(|port| Written(PortRange { from: port, to: port })),
        2 => (select(&PORTS[..]), select(&PORTS[..])).prop_map(|(lhs, rhs)| Written(PortRange {
            from: lhs.min(rhs),
            to: lhs.max(rhs),
        })),
        1 => Just(Written(PortRange { from: 0, to: u16::MAX })),
    ]
}

/// A ruleset of the networks, each set of a rule empty, matching every
/// value, a third of the time.
fn rules<N>(nets: impl Strategy<Value = Written<N>> + Clone) -> impl Strategy<Value = Vec<TestRule<N>>>
where
    N: Copy,
    Written<N>: Debug,
{
    let rule = (
        vec(nets.clone(), 0..=2),
        vec(nets, 0..=2),
        vec(proto_ranges(), 0..=2),
        vec(port_ranges(), 0..=2),
    )
        .prop_map(|(src, dst, protos, ports)| TestRule {
            src: unwritten(src),
            dst: unwritten(dst),
            protos: unwritten(protos),
            ports: unwritten(ports),
        });
    vec(rule, 1..=MAX_RULES)
}

/// A value of a rule, debugged as an operator writes it, so a shrunk ruleset
/// reads as a configuration.
#[derive(Clone, Copy)]
struct Written<T>(T);

/// The values of a set, unwrapped.
fn unwritten<T>(items: Vec<Written<T>>) -> Vec<T> {
    items.into_iter().map(|Written(item)| item).collect()
}

impl Debug for Written<Net4> {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let Self(Net4 { addr, mask }) = self;
        write!(
            f,
            "{}/{}",
            Ipv4Addr::from(*addr),
            u32::from_be_bytes(*mask).leading_ones()
        )
    }
}

// A prefix as `addr/len`, any other mask as `addr/high+low`, the lengths of
// its halves.
impl Debug for Written<Net6> {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let Self(Net6 { addr, mask }) = self;
        let mask = u128::from_be_bytes(*mask);
        let (high, low) = (((mask >> 64) as u64).leading_ones(), (mask as u64).leading_ones());
        let addr = Ipv6Addr::from(*addr);
        if high == 64 || low == 0 {
            write!(f, "{addr}/{}", high + low)
        } else {
            write!(f, "{addr}/{high}+{low}")
        }
    }
}

impl Debug for Written<LineRange> {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let Self(LineRange { from, to }) = self;
        if from == to {
            write!(f, "{from}")
        } else {
            write!(f, "{from}-{to}")
        }
    }
}

impl Debug for Written<PortRange> {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let Self(PortRange { from, to }) = self;
        if from == to {
            write!(f, "{from}")
        } else {
            write!(f, "{from}-{to}")
        }
    }
}

/// Writes a set of a rule, `*` when it is empty and matches every value.
fn write_set<T>(f: &mut Formatter<'_>, set: &[T]) -> Result<(), fmt::Error>
where
    T: Copy,
    Written<T>: Debug,
{
    if set.is_empty() {
        return f.write_str("*");
    }
    f.write_str("[")?;
    for (idx, &item) in set.iter().enumerate() {
        if idx > 0 {
            f.write_str(", ")?;
        }
        write!(f, "{:?}", Written(item))?;
    }
    f.write_str("]")
}

impl<N> Debug for TestRule<N>
where
    N: Copy,
    Written<N>: Debug,
{
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let Self { src, dst, protos, ports } = self;
        f.write_str("src ")?;
        write_set(f, src)?;
        f.write_str(" dst ")?;
        write_set(f, dst)?;
        f.write_str(" proto ")?;
        write_set::<LineRange>(f, protos)?;
        f.write_str(" port ")?;
        write_set::<PortRange>(f, ports)
    }
}

/// A splitmix64 generator: the random keys of a case follow from its seed.
struct Mix(u64);

impl Mix {
    fn next(&mut self) -> u64 {
        self.0 = self.0.wrapping_add(0x9e37_79b9_7f4a_7c15);
        let mut z = self.0;
        z = (z ^ (z >> 30)).wrapping_mul(0xbf58_476d_1ce4_e5b9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94d0_49bb_1331_11eb);
        z ^ (z >> 31)
    }

    fn pick<T>(&mut self, items: &[T]) -> T
    where
        T: Copy,
    {
        items[(self.next() % items.len() as u64) as usize]
    }
}

/// A key of a tree: the source, the destination, the protocol and the port.
type Key<A> = (A, A, u8, u16);

/// The protocols and the ports an edge address is looked up with: every edge
/// protocol and every edge port, paired in turn from random starts, and a few
/// random pairs.
fn transports(protos: &[u8], ports: &[u16], mix: &mut Mix) -> Vec<(u8, u16)> {
    let (proto_at, port_at) = (mix.next() as usize, mix.next() as usize);
    let paired = (0..protos.len().max(ports.len())).map(|idx| {
        (
            protos[proto_at.wrapping_add(idx) % protos.len()],
            ports[port_at.wrapping_add(idx) % ports.len()],
        )
    });
    let mut transports = paired.collect::<Vec<_>>();
    transports.extend((0..PARTNERS).map(|_| (mix.pick(protos), mix.pick(ports))));
    transports
}

/// The keys of a case, of the edge values of each field.
///
/// Every edge source and every edge destination meets every edge protocol
/// and every edge port, the other address picked at random from its edges;
/// every edge protocol meets every edge port. Then keys picked at random, a
/// quarter of their fields from the whole domain.
fn keys<A>(
    srcs: &[A],
    dsts: &[A],
    protos: &[u8],
    ports: &[u16],
    mix: &mut Mix,
    random: impl Fn(&mut Mix) -> A,
) -> Vec<Key<A>>
where
    A: Copy,
{
    let mut keys = Vec::new();
    for &src in srcs {
        for (proto, port) in transports(protos, ports, mix) {
            keys.push((src, mix.pick(dsts), proto, port));
        }
    }
    for &dst in dsts {
        for (proto, port) in transports(protos, ports, mix) {
            keys.push((mix.pick(srcs), dst, proto, port));
        }
    }
    for &proto in protos {
        for &port in ports {
            keys.push((mix.pick(srcs), mix.pick(dsts), proto, port));
        }
    }
    for _ in 0..RANDOM_KEYS {
        let addr = |addrs: &[A], mix: &mut Mix| {
            if mix.next().is_multiple_of(4) {
                random(mix)
            } else {
                mix.pick(addrs)
            }
        };
        let (src, dst) = (addr(srcs, mix), addr(dsts, mix));
        let proto = if mix.next().is_multiple_of(4) {
            mix.next() as u8
        } else {
            mix.pick(protos)
        };
        let port = if mix.next().is_multiple_of(4) {
            mix.next() as u16
        } else {
            mix.pick(ports)
        };
        keys.push((src, dst, proto, port));
    }
    keys
}

/// The IPv4 addresses at the edges of the networks, and the ends of the
/// domain.
fn edges4<'a>(nets: impl Iterator<Item = &'a Net4>) -> Vec<Ipv4Addr> {
    let mut addrs = vec![0, u32::MAX];
    for net in nets {
        let (addr, mask) = (u32::from_be_bytes(net.addr), u32::from_be_bytes(net.mask));
        let (first, last) = (addr & mask, addr | !mask);
        addrs.extend([first, last, first.wrapping_sub(1), last.wrapping_add(1)]);
    }
    addrs.sort_unstable();
    addrs.dedup();
    addrs.into_iter().map(Ipv4Addr::from).collect()
}

/// The IPv6 addresses whose halves are at the edges of the halves of the
/// networks, or at the ends of a half: every high edge under every low edge.
///
/// A network ranges its halves apart, so a high half at the edge of one
/// network meets the low halves at the edges of every other.
fn split_edges6<'a>(nets: impl Iterator<Item = &'a Net6>) -> Vec<Ipv6Addr> {
    let (mut highs, mut lows) = (vec![0, u64::MAX], vec![0, u64::MAX]);
    for net in nets {
        let (addr, mask) = (u128::from_be_bytes(net.addr), u128::from_be_bytes(net.mask));
        for (half, mask, edges) in [
            ((addr >> 64) as u64, (mask >> 64) as u64, &mut highs),
            (addr as u64, mask as u64, &mut lows),
        ] {
            let (first, last) = (half & mask, half | !mask);
            edges.extend([first, last, first.wrapping_sub(1), last.wrapping_add(1)]);
        }
    }
    for edges in [&mut highs, &mut lows] {
        edges.sort_unstable();
        edges.dedup();
    }
    highs
        .iter()
        .flat_map(|&high| {
            lows.iter()
                .map(move |&low| Ipv6Addr::from((u128::from(high) << 64) | u128::from(low)))
        })
        .collect()
}

/// The first key whose lookup or burst lookup is not its first matching
/// rule, written with the three answers.
fn mismatch<A, K>(
    keys: &[Key<A>],
    expected: impl Fn(Key<A>) -> Option<usize>,
    tree_key: impl Fn(Key<A>) -> K,
    lookup: impl Fn(K) -> Rule,
    lookup_burst: impl Fn(&[K], &mut [Rule]),
) -> Option<String>
where
    A: Copy + Debug,
    K: Copy,
{
    let tree_keys = keys.iter().map(|&key| tree_key(key)).collect::<Vec<_>>();
    let mut burst = vec![Rule::NONE; tree_keys.len()];
    lookup_burst(&tree_keys, &mut burst);
    keys.iter()
        .zip(tree_keys)
        .zip(burst)
        .find_map(|((&key, tree_key), burst)| {
            let expected = expected(key);
            let (single, burst) = (lookup(tree_key).index(), burst.index());
            (single != expected || burst != expected)
                .then(|| format!("key {key:?}: first matching rule {expected:?}, lookup {single:?}, burst {burst:?}"))
        })
}

/// The run of a property and its seed: the rulesets `PROPTEST_CASES` asks
/// for, the seed `PROPTEST_RNG_SEED` gives or a random one, and no failure
/// file in the source tree.
fn config() -> (Config, u64) {
    let cases = env::var("PROPTEST_CASES")
        .ok()
        .and_then(|cases| cases.parse().ok())
        .unwrap_or(CASES);
    let config = Config::default();
    let seed = match config.rng_seed {
        RngSeed::Fixed(seed) => seed,
        RngSeed::Random => RandomState::new().build_hasher().finish(),
    };
    let config = Config {
        cases,
        failure_persistence: None,
        rng_seed: RngSeed::Fixed(seed),
        ..config
    };
    (config, seed)
}

/// Runs a check over random rulesets, each with the seed of its random keys,
/// and panics with the shrunk ruleset of the first failure.
///
/// The shrinker deletes each rule once, before it simplifies the others, so
/// rules that fail to delete then may be needless once the rest is simple:
/// each is tried again, the seed kept.
fn run<N>(
    nets: impl Strategy<Value = Written<N>> + Clone,
    check: impl Fn((Vec<TestRule<N>>, u64)) -> Result<(), TestCaseError>,
) where
    N: Copy,
    Written<N>: Debug,
{
    let (config, rng_seed) = config();
    let mut runner = TestRunner::new(config);
    let Err(err) = runner.run(&(rules(nets), any::<u64>()), &check) else {
        return;
    };
    let TestError::Fail(mut reason, (mut rules, seed)) = err else {
        panic!("{err}\nPROPTEST_RNG_SEED={rng_seed}");
    };
    let mut idx = 0;
    while idx < rules.len() && rules.len() > 1 {
        let mut fewer = rules.clone();
        fewer.remove(idx);
        match panic::catch_unwind(AssertUnwindSafe(|| check((fewer.clone(), seed)))) {
            Ok(Err(TestCaseError::Fail(why))) => (rules, reason) = (fewer, why),
            _ => idx += 1,
        }
    }
    panic!(
        "{}\nPROPTEST_RNG_SEED={rng_seed}",
        TestError::Fail(reason, (rules, seed))
    );
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_rules_tree4_lookups_are_the_first_matching_rule_of_random_rulesets() {
    let owner = ArenaOwner::new();
    run(net4s(), |(rules, seed)| {
        let built = build4(&owner, &rules);
        let tree = owner.body(&built).get(&owner);
        let (protos, ports) = edges(&rules);
        let srcs = edges4(rules.iter().flat_map(|rule| &rule.src));
        let dsts = edges4(rules.iter().flat_map(|rule| &rule.dst));
        let keys = keys(&srcs, &dsts, &protos, &ports, &mut Mix(seed), |mix| {
            Ipv4Addr::from(mix.next() as u32)
        });
        let found = mismatch(
            &keys,
            |(src, dst, proto, port)| {
                first(
                    &rules,
                    |rule| {
                        any_or(&rule.src, |net| in4(net, src.into())) && any_or(&rule.dst, |net| in4(net, dst.into()))
                    },
                    proto,
                    port,
                )
            },
            |(src, dst, proto, port)| ((Addr4::from(src), Addr4::from(dst)), (proto, port)),
            |key| tree.lookup(&owner, key),
            |keys, rules| tree.lookup_burst(&owner, keys, |&key| key, rules),
        );
        // SAFETY: built by this owner, and no lookup outlives it.
        unsafe { built.destroy(&owner) };
        found.map_or(Ok(()), |found| Err(TestCaseError::fail(found)))
    });
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_rules_tree6_lookups_are_the_first_matching_rule_of_random_rulesets() {
    let owner = ArenaOwner::new();
    run(net6s(), |(rules, seed)| {
        let built = build6(&owner, &rules);
        let tree = owner.body(&built).get(&owner);
        let (protos, ports) = edges(&rules);
        let srcs = split_edges6(rules.iter().flat_map(|rule| &rule.src));
        let dsts = split_edges6(rules.iter().flat_map(|rule| &rule.dst));
        let keys = keys(&srcs, &dsts, &protos, &ports, &mut Mix(seed), |mix| {
            Ipv6Addr::from((u128::from(mix.next()) << 64) | u128::from(mix.next()))
        });
        let found = mismatch(
            &keys,
            |(src, dst, proto, port)| {
                first(
                    &rules,
                    |rule| {
                        any_or(&rule.src, |net| in6(net, src.into())) && any_or(&rule.dst, |net| in6(net, dst.into()))
                    },
                    proto,
                    port,
                )
            },
            |(src, dst, proto, port)| ((src.octets(), dst.octets()), (proto, port)),
            |key| tree.lookup(&owner, key),
            |keys, rules| tree.lookup_burst(&owner, keys, |&key| key, rules),
        );
        // SAFETY: built by this owner, and no lookup outlives it.
        unsafe { built.destroy(&owner) };
        found.map_or(Ok(()), |found| Err(TestCaseError::fail(found)))
    });
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_tree_build_and_release_of_random_rulesets_leave_the_arena_as_they_found_it() {
    let owner = ArenaOwner::new();
    // Each case against its own baseline, so a leak fails only its cases.
    let released = |before: usize| {
        let after = owner.free_bytes();
        (after == before)
            .then_some(())
            .ok_or_else(|| TestCaseError::fail(format!("{after} bytes free after a release, {before} before")))
    };
    run(net4s(), |(rules, _)| {
        let before = owner.free_bytes();
        // SAFETY: built by this owner and referenced by nothing.
        unsafe { build4(&owner, &rules).destroy(&owner) };
        released(before)
    });
    run(net6s(), |(rules, _)| {
        let before = owner.free_bytes();
        // SAFETY: built by this owner and referenced by nothing.
        unsafe { build6(&owner, &rules).destroy(&owner) };
        released(before)
    });
}
