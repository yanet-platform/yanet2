//! The classifier trees against the first rule each key matches, over
//! classifiers the C compiler builds through the builder in a C block arena.
//!
//! A tree reads its joints, rule tables and rule maps unchecked, so these
//! tests hold every lookup to an oracle that matches the rules one by one,
//! at the edges of every network and range of the rules. Natively only: Miri
//! does not run C.

#[path = "support/arena.rs"]
mod arena;

use core::net::Ipv4Addr;

use arena::ArenaOwner;
// The workspace denies unused crate dependencies; this test names no crate it
// imports `as _`.
use netip as _;
use yanet_sdk_testing as _;
use yanet_shm::rel::ShmBox;
use yanet_sys::{
    classify::{
        Addr4, BURST, Decode, Join, LineAttr, LineRange, Lines, Net4, Net4Attr, Net6, Net6Attr, Nets4, Nets6, Paths,
        PortAttr, PortRange, Ports, Rule, Rules,
    },
    cp::{BuildError, Builder, Built, Refusal},
    dp::{Batch, CommitCx, Cx, Module},
};

type Error = BuildError<arena::Never>;

/// The tree of the IPv4 rules: the networks joined, the protocol and the
/// port joined, and the rule of each pair.
type Tree4 = Rules<Join<Net4Attr, Net4Attr>, Join<LineAttr, PortAttr>>;

/// The tree of the IPv6 rules, shaped as the IPv4 one.
type Tree6 = Rules<Join<Net6Attr, Net6Attr>, Join<LineAttr, PortAttr>>;

/// The tree of the rules of any family: the protocol and the port joined
/// and decoded.
type TreeAny = Decode<Join<LineAttr, PortAttr>>;

/// A module whose configuration is one tree; never run.
struct Tree<T>(T);

macro_rules! tree_module {
    ($($tree:ty: $name:literal;)*) => {$(
        impl Module for Tree<$tree> {
            const NAME: &'static str = $name;
            type Config = ShmBox<$tree>;
            type Prepared<'p> = ();

            fn handle<'id>(_: &mut Cx<'id, Self>, _: &mut Batch<'id>) {}

            fn prepare<'p>(_: &CommitCx<'p, Self>) {}
        }
    )*};
}

/// The IPv4 rules on a TCP path and a UDP path: the networks joined into the
/// core, the destination ports of the rules that restrict one.
type Paths4 = Paths<Join<Net4Attr, Net4Attr>, PortAttr, 2>;

/// The protocols of the paths of [`Paths4`], by path.
const PATHS: [u8; 2] = [6, 17];

tree_module! {
    Paths4: "classify-paths-v4";
    Tree4: "classify-tree-v4";
    Tree6: "classify-tree-v6";
    TreeAny: "classify-tree-any";
}

/// The IPv4 network of an address and a prefix length.
fn net4(addr: [u8; 4], len: u32) -> Net4 {
    let mask = u32::MAX.checked_shl(32 - len).unwrap_or(0);
    Net4 { addr, mask: mask.to_be_bytes() }
}

/// The IPv6 network of an address and a prefix length.
fn net6(addr: u128, len: u32) -> Net6 {
    let mask = u128::MAX.checked_shl(128 - len).unwrap_or(0);
    Net6 {
        addr: addr.to_be_bytes(),
        mask: mask.to_be_bytes(),
    }
}

/// A rule of the tests: its networks, of one family, its protocols and its
/// destination ports, an empty set matching every value.
struct TestRule<N> {
    src: Vec<N>,
    dst: Vec<N>,
    protos: Vec<LineRange>,
    ports: Vec<PortRange>,
}

const TCP: LineRange = LineRange { from: 6, to: 6 };
const UDP: LineRange = LineRange { from: 17, to: 17 };

fn port(from: u16, to: u16) -> PortRange {
    PortRange { from, to }
}

/// The IPv4 rules of the tests, in order of precedence: overlapping
/// networks, ranges and single values, and a rule that matches everything.
fn rules4() -> Vec<TestRule<Net4>> {
    vec![
        TestRule {
            src: vec![net4([10, 0, 0, 0], 8)],
            dst: vec![net4([192, 0, 2, 0], 24)],
            protos: vec![TCP],
            ports: vec![port(80, 80), port(443, 443)],
        },
        TestRule {
            src: vec![],
            dst: vec![net4([192, 0, 2, 128], 25), net4([198, 51, 100, 0], 24)],
            protos: vec![LineRange { from: 6, to: 17 }],
            ports: vec![port(1000, 2000)],
        },
        TestRule {
            src: vec![net4([10, 1, 0, 0], 16)],
            dst: vec![],
            protos: vec![UDP],
            ports: vec![port(53, 53)],
        },
        TestRule {
            src: vec![],
            dst: vec![net4([203, 0, 113, 7], 32)],
            protos: vec![],
            ports: vec![],
        },
        TestRule {
            src: vec![],
            dst: vec![net4([0, 0, 0, 0], 0)],
            protos: vec![TCP],
            ports: vec![port(22, 22)],
        },
    ]
}

/// The IPv6 rules of the tests: networks that split the low halves of one
/// high half, and a rule that matches everything.
fn rules6() -> Vec<TestRule<Net6>> {
    let doc6 = 0x2001_0db8_u128 << 96;
    vec![
        TestRule {
            src: vec![net6(doc6, 32)],
            dst: vec![net6(doc6 | (1 << 63), 65)],
            protos: vec![UDP],
            ports: vec![port(53, 53)],
        },
        TestRule {
            src: vec![net6(doc6 | (1 << 80), 48)],
            dst: vec![],
            protos: vec![],
            ports: vec![],
        },
        TestRule {
            src: vec![],
            dst: vec![net6(doc6 | 1, 128)],
            protos: vec![TCP],
            ports: vec![port(1, 1024)],
        },
        TestRule {
            src: vec![],
            dst: vec![],
            protos: vec![],
            ports: vec![port(8080, 8080)],
        },
    ]
}

/// Whether a set matches a value: an empty one matches every value.
fn any_or<T>(set: &[T], matches: impl Fn(&T) -> bool) -> bool {
    set.is_empty() || set.iter().any(matches)
}

fn in4(net: &Net4, addr: u32) -> bool {
    let mask = u32::from_be_bytes(net.mask);
    addr & mask == u32::from_be_bytes(net.addr) & mask
}

fn in6(net: &Net6, addr: u128) -> bool {
    let mask = u128::from_be_bytes(net.mask);
    addr & mask == u128::from_be_bytes(net.addr) & mask
}

/// The index of the first rule that matches the protocol and the port, of
/// those the network test admits.
fn first<N>(rules: &[TestRule<N>], nets: impl Fn(&TestRule<N>) -> bool, proto: u8, port: u16) -> Option<usize> {
    rules.iter().position(|rule| {
        nets(rule)
            && any_or(&rule.protos, |range| {
                (range.from..=range.to).contains(&u32::from(proto))
            })
            && any_or(&rule.ports, |range| (range.from..=range.to).contains(&port))
    })
}

/// Builds the IPv4 tree of the rules.
fn build4(owner: &ArenaOwner, rules: &[TestRule<Net4>]) -> Built<Tree<Tree4>, ArenaOwner> {
    let views = rules
        .iter()
        .map(|rule| {
            (
                Nets4::new(&rule.src).unwrap(),
                Nets4::new(&rule.dst).unwrap(),
                Lines::new(&rule.protos).unwrap(),
                Ports::new(&rule.ports).unwrap(),
            )
        })
        .collect::<Vec<_>>();
    let src = views.iter().map(|view| Some(&view.0)).collect::<Vec<_>>();
    let dst = views.iter().map(|view| Some(&view.1)).collect::<Vec<_>>();
    let protos = views.iter().map(|view| Some(&view.2)).collect::<Vec<_>>();
    let ports = views.iter().map(|view| Some(&view.3)).collect::<Vec<_>>();
    let projection = views.iter().map(|_| Some(&())).collect::<Vec<_>>();
    Builder::build(owner, "v4", |scope| -> Result<_, Error> {
        let src = scope.compile(c"src", &src)?;
        let dst = scope.compile(c"dst", &dst)?;
        let proto = scope.compile(c"proto", &protos)?;
        let port = scope.compile(c"port", &ports)?;
        let nets = scope.join(c"nets", src, dst)?;
        let transport = scope.join(c"transport", proto, port)?;
        scope.join_rules(c"rules", nets, transport, &projection)
    })
    .unwrap()
}

/// Builds the IPv6 tree of the rules.
fn build6(owner: &ArenaOwner, rules: &[TestRule<Net6>]) -> Built<Tree<Tree6>, ArenaOwner> {
    let views = rules
        .iter()
        .map(|rule| {
            (
                Nets6::new(&rule.src).unwrap(),
                Nets6::new(&rule.dst).unwrap(),
                Lines::new(&rule.protos).unwrap(),
                Ports::new(&rule.ports).unwrap(),
            )
        })
        .collect::<Vec<_>>();
    let src = views.iter().map(|view| Some(&view.0)).collect::<Vec<_>>();
    let dst = views.iter().map(|view| Some(&view.1)).collect::<Vec<_>>();
    let protos = views.iter().map(|view| Some(&view.2)).collect::<Vec<_>>();
    let ports = views.iter().map(|view| Some(&view.3)).collect::<Vec<_>>();
    let projection = views.iter().map(|_| Some(&())).collect::<Vec<_>>();
    Builder::build(owner, "v6", |scope| -> Result<_, Error> {
        let src = scope.compile(c"src", &src)?;
        let dst = scope.compile(c"dst", &dst)?;
        let proto = scope.compile(c"proto", &protos)?;
        let port = scope.compile(c"port", &ports)?;
        let nets = scope.join(c"nets", src, dst)?;
        let transport = scope.join(c"transport", proto, port)?;
        scope.join_rules(c"rules", nets, transport, &projection)
    })
    .unwrap()
}

/// The protocols and the ports every tree is looked up at: the edges of
/// every range of the rules, and the ends of the domains.
fn edges<N>(rules: &[TestRule<N>]) -> (Vec<u8>, Vec<u16>) {
    let mut protos = vec![0, u8::MAX];
    let mut ports = vec![0, u16::MAX];
    for rule in rules {
        for range in &rule.protos {
            let around = [
                range.from.checked_sub(1),
                Some(range.from),
                Some(range.to),
                range.to.checked_add(1),
            ];
            protos.extend(
                around
                    .into_iter()
                    .flatten()
                    .filter_map(|proto| u8::try_from(proto).ok()),
            );
        }
        for range in &rule.ports {
            ports.extend([range.from, range.to]);
            ports.extend(range.from.checked_sub(1));
            ports.extend(range.to.checked_add(1));
        }
    }
    protos.sort_unstable();
    protos.dedup();
    ports.sort_unstable();
    ports.dedup();
    (protos, ports)
}

/// Builds the paths of the IPv4 rules.
fn build_paths4(owner: &ArenaOwner, rules: &[TestRule<Net4>]) -> Built<Tree<Paths4>, ArenaOwner> {
    let views = rules
        .iter()
        .map(|rule| {
            (
                Nets4::new(&rule.src).unwrap(),
                Nets4::new(&rule.dst).unwrap(),
                Ports::new(&rule.ports).unwrap(),
            )
        })
        .collect::<Vec<_>>();
    let src = views.iter().map(|view| Some(&view.0)).collect::<Vec<_>>();
    let dst = views.iter().map(|view| Some(&view.1)).collect::<Vec<_>>();
    // The ports over the rules that restrict one only.
    let ports = views
        .iter()
        .zip(rules)
        .map(|(view, rule)| (!rule.ports.is_empty()).then_some(&view.2))
        .collect::<Vec<_>>();
    let on = |proto: u8, ported: bool| {
        rules
            .iter()
            .map(|rule| {
                let of = any_or(&rule.protos, |range| {
                    (range.from..=range.to).contains(&u32::from(proto))
                });
                (of && rule.ports.is_empty() != ported).then_some(&())
            })
            .collect::<Vec<_>>()
    };
    let [tcp, udp] = PATHS.map(|proto| (on(proto, true), on(proto, false)));
    Builder::build(owner, "paths", |scope| -> Result<_, Error> {
        let src = scope.compile(c"src", &src)?;
        let dst = scope.compile(c"dst", &dst)?;
        let port = scope.compile(c"port", &ports)?;
        let core = scope.join(c"core", src, dst)?;
        scope.join_paths(c"paths", core, port, [(&tcp.0, &tcp.1), (&udp.0, &udp.1)])
    })
    .unwrap()
}

/// The IPv4 addresses at the edges of every network of the rules.
fn edges4(rules: &[TestRule<Net4>]) -> Vec<u32> {
    let mut addrs = vec![0, u32::MAX, u32::from(Ipv4Addr::new(10, 200, 0, 1))];
    for net in rules.iter().flat_map(|rule| rule.src.iter().chain(&rule.dst)) {
        let (addr, mask) = (u32::from_be_bytes(net.addr), u32::from_be_bytes(net.mask));
        let (first, last) = (addr & mask, addr | !mask);
        addrs.extend([first, last, first.wrapping_sub(1), last.wrapping_add(1)]);
    }
    addrs.sort_unstable();
    addrs.dedup();
    addrs
}

/// The IPv6 addresses at the edges of every network of the rules, in each
/// half.
fn edges6(rules: &[TestRule<Net6>]) -> Vec<u128> {
    let mut addrs = vec![0, u128::MAX];
    for net in rules.iter().flat_map(|rule| rule.src.iter().chain(&rule.dst)) {
        let (addr, mask) = (u128::from_be_bytes(net.addr), u128::from_be_bytes(net.mask));
        let (first, last) = (addr & mask, addr | !mask);
        addrs.extend([first, last, first.wrapping_sub(1), last.wrapping_add(1)]);
        // The halves split apart: the low half's edges under another high
        // half.
        addrs.extend([first ^ (1 << 64), last ^ (1 << 64)]);
    }
    addrs.sort_unstable();
    addrs.dedup();
    addrs
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_rules_tree4_lookup_is_the_first_matching_rule() {
    let owner = ArenaOwner::new();
    let rules = rules4();
    let built = build4(&owner, &rules);
    let tree = owner.body(&built).get(&owner);
    let addrs = edges4(&rules);
    let (protos, ports) = edges(&rules);
    for &src in &addrs {
        for &dst in &addrs {
            for &proto in &protos {
                for &port in &ports {
                    let expected = first(
                        &rules,
                        |rule| any_or(&rule.src, |net| in4(net, src)) && any_or(&rule.dst, |net| in4(net, dst)),
                        proto,
                        port,
                    );
                    let key = (
                        (Addr4::from(src.to_be_bytes()), Addr4::from(dst.to_be_bytes())),
                        (proto, port),
                    );
                    assert_eq!(
                        expected,
                        tree.lookup(&owner, key).index(),
                        "{} {} {proto} {port}",
                        Ipv4Addr::from(src),
                        Ipv4Addr::from(dst)
                    );
                }
            }
        }
    }
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_rules_tree6_lookup_is_the_first_matching_rule() {
    let owner = ArenaOwner::new();
    let rules = rules6();
    let built = build6(&owner, &rules);
    let tree = owner.body(&built).get(&owner);
    let addrs = edges6(&rules);
    let (protos, ports) = edges(&rules);
    for &src in &addrs {
        for &dst in &addrs {
            for &proto in &protos {
                for &port in &ports {
                    let expected = first(
                        &rules,
                        |rule| any_or(&rule.src, |net| in6(net, src)) && any_or(&rule.dst, |net| in6(net, dst)),
                        proto,
                        port,
                    );
                    let key = ((src.to_be_bytes(), dst.to_be_bytes()), (proto, port));
                    assert_eq!(
                        expected,
                        tree.lookup(&owner, key).index(),
                        "{src:x} {dst:x} {proto} {port}"
                    );
                }
            }
        }
    }
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_decode_tree_lookup_is_the_first_rule_of_the_projection() {
    let owner = ArenaOwner::new();
    let rules = rules4();
    let views = rules
        .iter()
        .map(|rule| (Lines::new(&rule.protos).unwrap(), Ports::new(&rule.ports).unwrap()))
        .collect::<Vec<_>>();
    let protos = views.iter().map(|view| Some(&view.0)).collect::<Vec<_>>();
    let ports = views.iter().map(|view| Some(&view.1)).collect::<Vec<_>>();
    // Every other rule is in the projection.
    let projection = (0..rules.len())
        .map(|idx| (idx % 2 == 1).then_some(&()))
        .collect::<Vec<_>>();
    let built: Built<Tree<TreeAny>, ArenaOwner> = Builder::build(&owner, "any", |scope| -> Result<_, Error> {
        let proto = scope.compile(c"proto", &protos)?;
        let port = scope.compile(c"port", &ports)?;
        let transport = scope.join(c"transport", proto, port)?;
        scope.decode(c"rules", transport, &projection)
    })
    .unwrap();
    let tree = owner.body(&built).get(&owner);
    let (edge_protos, edge_ports) = edges(&rules);
    for &proto in &edge_protos {
        for &port in &edge_ports {
            let expected = rules.iter().enumerate().position(|(idx, rule)| {
                idx % 2 == 1
                    && any_or(&rule.protos, |range| {
                        (range.from..=range.to).contains(&u32::from(proto))
                    })
                    && any_or(&rule.ports, |range| (range.from..=range.to).contains(&port))
            });
            assert_eq!(expected, tree.lookup(&owner, (proto, port)).index(), "{proto} {port}");
        }
    }
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_rules_tree_of_an_empty_projection_is_no_rule() {
    let owner = ArenaOwner::new();
    let rules = rules4();
    let views = rules
        .iter()
        .map(|rule| {
            (
                Nets4::new(&rule.src).unwrap(),
                Nets4::new(&rule.dst).unwrap(),
                Lines::new(&rule.protos).unwrap(),
                Ports::new(&rule.ports).unwrap(),
            )
        })
        .collect::<Vec<_>>();
    // No rule is of the family: every attribute compiles over none.
    let none4: Vec<Option<&Nets4<'_>>> = views.iter().map(|_| None).collect();
    let none_lines: Vec<Option<&Lines<'_>>> = views.iter().map(|_| None).collect();
    let none_ports: Vec<Option<&Ports<'_>>> = views.iter().map(|_| None).collect();
    let projection: Vec<Option<&()>> = views.iter().map(|_| None).collect();
    let built: Built<Tree<Tree4>, ArenaOwner> = Builder::build(&owner, "empty", |scope| -> Result<_, Error> {
        let src = scope.compile(c"src", &none4)?;
        let dst = scope.compile(c"dst", &none4)?;
        let proto = scope.compile(c"proto", &none_lines)?;
        let port = scope.compile(c"port", &none_ports)?;
        let nets = scope.join(c"nets", src, dst)?;
        let transport = scope.join(c"transport", proto, port)?;
        scope.join_rules(c"rules", nets, transport, &projection)
    })
    .unwrap();
    let tree = owner.body(&built).get(&owner);
    for addr in edges4(&rules) {
        let key = (
            (Addr4::from(addr.to_be_bytes()), Addr4::from(addr.to_be_bytes())),
            (6, 80),
        );
        assert_eq!(Rule::NONE, tree.lookup(&owner, key));
    }
}

#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_join_refuses_branches_of_rulesets_of_other_sizes() {
    let owner = ArenaOwner::new();
    let lines = Lines::new(&[TCP]).unwrap();
    let http = [port(80, 80)];
    let ports = Ports::new(&http).unwrap();
    let result: Result<Built<Tree<TreeAny>, ArenaOwner>, Error> = Builder::build(&owner, "mismatch", |scope| {
        let proto = scope.compile(c"proto", &[Some(&lines)])?;
        let port = scope.compile(c"port", &[Some(&ports), Some(&ports)])?;
        let transport = scope.join(c"transport", proto, port)?;
        scope.decode(c"rules", transport, &[Some(&())])
    });
    assert!(matches!(result, Err(BuildError::Refused(Refusal::RuleSetMismatch))));
}

/// verifies that the IPv4 tree reports the first matching rule over every
/// port of the protocols of the rules and every protocol of a few ports, not
/// only at the edges of their ranges.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_rules_tree4_lookup_is_the_first_matching_rule_over_whole_domains() {
    let owner = ArenaOwner::new();
    let rules = rules4();
    let built = build4(&owner, &rules);
    let tree = owner.body(&built).get(&owner);
    let pairs = [
        ([10, 0, 0, 1], [192, 0, 2, 200]),
        ([10, 1, 2, 3], [198, 51, 100, 5]),
        ([1, 2, 3, 4], [203, 0, 113, 7]),
    ];
    let keys = (0..=u16::MAX)
        .flat_map(|port| [0, 6, 17].map(|proto| (proto, port)))
        .chain((0..=u8::MAX).flat_map(|proto| [22, 53, 80, 1500].map(|port| (proto, port))));
    for (proto, port) in keys {
        for (src, dst) in pairs {
            let (src, dst) = (u32::from_be_bytes(src), u32::from_be_bytes(dst));
            let expected = first(
                &rules,
                |rule| any_or(&rule.src, |net| in4(net, src)) && any_or(&rule.dst, |net| in4(net, dst)),
                proto,
                port,
            );
            let key = (
                (Addr4::from(src.to_be_bytes()), Addr4::from(dst.to_be_bytes())),
                (proto, port),
            );
            assert_eq!(expected, tree.lookup(&owner, key).index(), "{proto} {port}");
        }
    }
}

/// verifies that the IPv6 tree tells apart two destinations of one high
/// half on either side of a network that splits the low half, which only the
/// table of the rows of split high halves does.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_rules_tree6_lookup_splits_a_high_half_by_its_low_half() {
    let owner = ArenaOwner::new();
    let built = build6(&owner, &rules6());
    let tree = owner.body(&built).get(&owner);
    let doc6 = 0x2001_0db8_u128 << 96;
    let lookup = |dst: u128| {
        let key = ((doc6.to_be_bytes(), dst.to_be_bytes()), (UDP.from as u8, 53));
        tree.lookup(&owner, key).index()
    };
    // The first rule's destination is the upper half of the low half's
    // range of its high half.
    assert_eq!((Some(0), None), (lookup(doc6 | (1 << 63) | 2), lookup(doc6 | 2)));
}

/// verifies that the paths report on the path of a protocol the first rule
/// of that protocol a key matches, one key at a time and a burst at a time.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_paths4_lookup_is_the_first_matching_rule_of_the_path() {
    let owner = ArenaOwner::new();
    let rules = rules4();
    let built = build_paths4(&owner, &rules);
    let paths = owner.body(&built).get(&owner);
    let addrs = edges4(&rules);
    let (_, ports) = edges(&rules);
    let mut keys = Vec::new();
    for &src in &addrs {
        for &dst in &addrs {
            for (path, proto) in PATHS.into_iter().enumerate() {
                for &port in &ports {
                    keys.push((path, proto, src, dst, port));
                }
            }
        }
    }
    let key = |&(path, _, src, dst, port): &(usize, u8, u32, u32, u16)| {
        (
            path,
            ((Addr4::from(src.to_be_bytes()), Addr4::from(dst.to_be_bytes())), port),
        )
    };
    let expected = keys
        .iter()
        .map(|&(_, proto, src, dst, port)| {
            first(
                &rules,
                |rule| any_or(&rule.src, |net| in4(net, src)) && any_or(&rule.dst, |net| in4(net, dst)),
                proto,
                port,
            )
        })
        .collect::<Vec<_>>();
    let found = keys
        .iter()
        .map(|item| {
            let (path, key) = key(item);
            paths.lookup(&owner, path, key).index()
        })
        .collect::<Vec<_>>();
    assert_eq!(expected, found);
    let mut burst = vec![Rule::NONE; keys.len()];
    paths.lookup_burst(&owner, &keys, key, &mut burst);
    assert_eq!(expected, burst.iter().map(|rule| rule.index()).collect::<Vec<_>>());
}

/// verifies that a burst lookup reports for each key the rule a lookup of the
/// key alone does, over bursts of every length up to a full one and a short
/// last one.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_rules_tree4_lookup_burst_is_the_lookup_of_each_key() {
    let owner = ArenaOwner::new();
    let rules = rules4();
    let built = build4(&owner, &rules);
    let tree = owner.body(&built).get(&owner);
    let addrs = edges4(&rules);
    let (protos, ports) = edges(&rules);
    let mut keys = Vec::new();
    for (idx, &src) in addrs.iter().enumerate() {
        for &dst in &addrs {
            let (proto, port) = (protos[idx % protos.len()], ports[(idx * 7) % ports.len()]);
            keys.push((
                (Addr4::from(src.to_be_bytes()), Addr4::from(dst.to_be_bytes())),
                (proto, port),
            ));
        }
    }
    // Not a whole number of bursts.
    keys.truncate(keys.len() / BURST * BURST - 5);
    let expected = keys.iter().map(|&key| tree.lookup(&owner, key)).collect::<Vec<_>>();
    for len in [1, 7, BURST, keys.len()] {
        let mut rules = vec![Rule::NONE; len];
        tree.lookup_burst(&owner, &keys[..len], |&key| key, &mut rules);
        assert_eq!(expected[..len], rules[..], "{len} keys");
    }
}

/// verifies that a burst decode reports for each key the rule a lookup of the
/// key alone does.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_decode_tree_lookup_burst_is_the_lookup_of_each_key() {
    let owner = ArenaOwner::new();
    let rules = rules4();
    let views = rules
        .iter()
        .map(|rule| (Lines::new(&rule.protos).unwrap(), Ports::new(&rule.ports).unwrap()))
        .collect::<Vec<_>>();
    let protos = views.iter().map(|view| Some(&view.0)).collect::<Vec<_>>();
    let ports = views.iter().map(|view| Some(&view.1)).collect::<Vec<_>>();
    let projection = views.iter().map(|_| Some(&())).collect::<Vec<_>>();
    let built: Built<Tree<TreeAny>, ArenaOwner> = Builder::build(&owner, "any", |scope| -> Result<_, Error> {
        let proto = scope.compile(c"proto", &protos)?;
        let port = scope.compile(c"port", &ports)?;
        let transport = scope.join(c"transport", proto, port)?;
        scope.decode(c"rules", transport, &projection)
    })
    .unwrap();
    let tree = owner.body(&built).get(&owner);
    let (edge_protos, edge_ports) = edges(&rules);
    let keys = edge_protos
        .iter()
        .flat_map(|&proto| edge_ports.iter().map(move |&port| (proto, port)))
        .collect::<Vec<_>>();
    let expected = keys.iter().map(|&key| tree.lookup(&owner, key)).collect::<Vec<_>>();
    let mut burst = vec![Rule::NONE; keys.len()];
    tree.lookup_burst(&owner, &keys, |&key| key, &mut burst);
    assert_eq!(expected, burst);
}

/// The views of the protocols and the ports of the IPv4 rules, and a
/// projection of every other rule.
struct AnyViews<'a> {
    views: Vec<(Lines<'a>, Ports<'a>)>,
}

impl<'a> AnyViews<'a> {
    fn new(rules: &'a [TestRule<Net4>]) -> Self {
        let views = rules
            .iter()
            .map(|rule| (Lines::new(&rule.protos).unwrap(), Ports::new(&rule.ports).unwrap()))
            .collect();
        Self { views }
    }

    fn protos(&self) -> Vec<Option<&Lines<'a>>> {
        self.views.iter().map(|view| Some(&view.0)).collect()
    }

    fn ports(&self) -> Vec<Option<&Ports<'a>>> {
        self.views.iter().map(|view| Some(&view.1)).collect()
    }

    fn projection(&self) -> Vec<Option<&()>> {
        (0..self.views.len()).map(|idx| (idx % 2 == 1).then_some(&())).collect()
    }
}

/// verifies that releasing a tree returns every block its build took from
/// the arena, and so does a build refused part way, its branches included.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_tree_build_and_release_leave_the_arena_as_they_found_it() {
    let owner = ArenaOwner::new();
    let rules = rules4();
    let baseline = owner.free_bytes();

    let built = build4(&owner, &rules);
    assert!(owner.free_bytes() < baseline, "a build takes blocks");
    // SAFETY: built by this owner and referenced by nothing.
    unsafe { built.destroy(&owner) };
    assert_eq!(baseline, owner.free_bytes(), "a released tree");

    let built = build_paths4(&owner, &rules);
    // SAFETY: as above.
    unsafe { built.destroy(&owner) };
    assert_eq!(baseline, owner.free_bytes(), "released paths");

    let views = AnyViews::new(&rules);
    let (protos, ports) = (views.protos(), views.ports());
    let refused: Result<Built<Tree<TreeAny>, ArenaOwner>, Error> = Builder::build(&owner, "refused", |scope| {
        let proto = scope.compile(c"proto", &protos)?;
        let port = scope.compile(c"port", &ports[1..])?;
        let transport = scope.join(c"transport", proto, port)?;
        scope.decode(c"rules", transport, &views.projection())
    });
    assert!(matches!(refused, Err(BuildError::Refused(Refusal::RuleSetMismatch))));
    assert_eq!(baseline, owner.free_bytes(), "a refused build");
}

/// verifies that a burst lookup of the IPv6 tree reports for each key the
/// rule a lookup of the key alone does.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_rules_tree6_lookup_burst_is_the_lookup_of_each_key() {
    let owner = ArenaOwner::new();
    let rules = rules6();
    let built = build6(&owner, &rules);
    let tree = owner.body(&built).get(&owner);
    let addrs = edges6(&rules);
    let (protos, ports) = edges(&rules);
    let keys = addrs
        .iter()
        .enumerate()
        .flat_map(|(idx, &src)| {
            let transport = (protos[idx % protos.len()], ports[(idx * 7) % ports.len()]);
            addrs
                .iter()
                .map(move |&dst| ((src.to_be_bytes(), dst.to_be_bytes()), transport))
        })
        .collect::<Vec<_>>();
    let expected = keys.iter().map(|&key| tree.lookup(&owner, key)).collect::<Vec<_>>();
    let mut burst = vec![Rule::NONE; keys.len()];
    tree.lookup_burst(&owner, &keys, |&key| key, &mut burst);
    assert_eq!(expected, burst);
}
