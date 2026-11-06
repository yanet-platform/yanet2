//! The Rust lookups of the classification against the C ones of the shim,
//! over classifiers the C compiler builds for rules of both families.
//!
//! The classifiers are compiled, joined and decoded through the shim in a C
//! block arena, as an agent builds them. Each stage is looked up both ways:
//! an attribute over its whole domain or at the edges of every network of the
//! rules, a joint, a rule table and a rule map over every pair of classes of
//! their stages and one past each end, as a class from the caller may be.
//! Natively only: Miri does not run C.

#[path = "support/arena.rs"]
#[allow(dead_code, reason = "these tests compile outside a configuration")]
mod arena;

use core::{ffi::CStr, fmt::Debug, mem::transmute};

use arena::ArenaOwner as Arena;
// The workspace denies unused crate dependencies; this test names no crate it
// imports `as _`.
use netip as _;
use yanet_sdk_testing as _;
use yanet_sys::{
    bindings,
    classify::{
        Addr4, Attribute, Joint, LineAttr, LineRange, Lines, Net4, Net4Attr, Net6, Net6Attr, Nets4, Nets6, PortAttr,
        PortRange, Ports, Rule, RuleMap, RuleTable, results_min,
    },
};

/// The source and destination networks of a rule, by family; a rule of any
/// family names none.
enum Nets {
    V4(Vec<Net4>, Vec<Net4>),
    V6(Vec<Net6>, Vec<Net6>),
    Any,
}

/// A rule of the tests: its networks, its protocols and its destination
/// ports, an empty set matching every value.
struct TestRule {
    nets: Nets,
    protos: Vec<LineRange>,
    ports: Vec<PortRange>,
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

/// The rules of the tests, in order of precedence.
///
/// Networks of both families overlap, IPv6 networks split the low halves of
/// one high half, rules of any family name no network, and protocols and
/// ports are single values and ranges.
fn rules() -> Vec<TestRule> {
    let (tcp, udp) = (LineRange { from: 6, to: 6 }, LineRange { from: 17, to: 17 });
    let port = |from: u16, to: u16| PortRange { from, to };
    let doc6 = 0x2001_0db8_u128 << 96;
    vec![
        TestRule {
            nets: Nets::V4(vec![net4([10, 0, 0, 0], 8)], vec![net4([192, 0, 2, 0], 24)]),
            protos: vec![tcp],
            ports: vec![port(80, 80), port(443, 443)],
        },
        TestRule {
            nets: Nets::V6(vec![net6(doc6, 32)], vec![net6(doc6 | (1 << 63), 65)]),
            protos: vec![udp],
            ports: vec![port(53, 53)],
        },
        TestRule {
            nets: Nets::Any,
            protos: vec![tcp],
            ports: vec![port(22, 22)],
        },
        TestRule {
            nets: Nets::V4(vec![], vec![net4([192, 0, 2, 128], 25), net4([198, 51, 100, 0], 24)]),
            protos: vec![LineRange { from: 6, to: 17 }],
            ports: vec![port(1000, 2000)],
        },
        TestRule {
            nets: Nets::V6(vec![net6(doc6 | (1 << 80), 48)], vec![]),
            protos: vec![],
            ports: vec![],
        },
        TestRule {
            nets: Nets::V6(vec![], vec![net6(doc6 | 1, 128)]),
            protos: vec![],
            ports: vec![],
        },
        TestRule {
            nets: Nets::Any,
            protos: vec![udp],
            ports: vec![],
        },
        TestRule {
            nets: Nets::V4(vec![], vec![net4([0, 0, 0, 0], 0)]),
            protos: vec![],
            ports: vec![],
        },
    ]
}

/// The count of a ruleset or a batch as C takes it.
fn count<T>(items: &[T]) -> u32 {
    u32::try_from(items.len()).expect("a count C holds")
}

/// A C object of a classification with the stage of its classes, both in the
/// arena.
type Staged<T> = (*mut T, *mut bindings::classifier);

/// Compiles the classifier of an attribute over the rules, `None` for a rule
/// outside the projection.
fn compile<A: Attribute>(arena: &Arena, name: &CStr, rules: &[Option<&A>]) -> Staged<A::Classifier> {
    let (classifier, stage) = (arena.alloc::<A::Classifier>(), arena.alloc::<bindings::classifier>());
    // SAFETY: a rule is a view of the attribute or none, borrowed for the
    // call; the classifier and the stage are zeroed blocks of the arena.
    let status = unsafe {
        A::compile(
            arena.raw_context(),
            name.as_ptr(),
            rules.as_ptr().cast(),
            count(rules),
            classifier,
            stage,
        )
    };
    assert_eq!(0, status, "compile {name:?}");
    (classifier, stage)
}

/// Joins the classes of two stages of a ruleset of the count.
fn join(
    arena: &Arena,
    name: &CStr,
    (left, right): (*mut bindings::classifier, *mut bindings::classifier),
    rules: u32,
) -> Staged<Joint> {
    let (joint, stage) = (arena.alloc::<Joint>(), arena.alloc::<bindings::classifier>());
    // SAFETY: both stages were filled over the ruleset; the joint and the
    // stage are zeroed blocks of the arena.
    let status = unsafe {
        bindings::yanet_sys_classify_join(
            arena.raw_context(),
            name.as_ptr(),
            left,
            right,
            rules,
            joint.cast(),
            stage,
        )
    };
    assert_eq!(0, status, "join {name:?}");
    (joint, stage)
}

/// The first rule of the projection each pair of classes of two stages
/// matches.
fn join_rules<R>(
    arena: &Arena,
    name: &CStr,
    (left, right): (*mut bindings::classifier, *mut bindings::classifier),
    rules: &[Option<&R>],
) -> *mut RuleTable {
    let table = arena.alloc::<RuleTable>();
    // SAFETY: both stages were filled over the ruleset of the projection; the
    // table is a zeroed block of the arena.
    let status = unsafe {
        bindings::yanet_sys_classify_join_rules(
            arena.raw_context(),
            name.as_ptr(),
            left,
            right,
            rules.as_ptr().cast(),
            count(rules),
            table.cast(),
        )
    };
    assert_eq!(0, status, "join rules {name:?}");
    table
}

/// The first rule of the projection each class of a stage matches.
fn decode<R>(arena: &Arena, name: &CStr, stage: *mut bindings::classifier, rules: &[Option<&R>]) -> *mut RuleMap {
    let map = arena.alloc::<RuleMap>();
    // SAFETY: the stage was filled over the ruleset of the projection; the
    // map is a zeroed block of the arena.
    let status = unsafe {
        bindings::yanet_sys_classify_decode(
            arena.raw_context(),
            name.as_ptr(),
            stage,
            rules.as_ptr().cast(),
            count(rules),
            map.cast(),
        )
    };
    assert_eq!(0, status, "decode {name:?}");
    map
}

/// The classifiers of one family's rules, as a module composes them.
///
/// The source and the destination join into the networks, the protocol and
/// the port into the transport, and both into the rule table.
struct Family<C> {
    src: *mut C,
    dst: *mut C,
    proto: *mut LineAttr,
    port: *mut PortAttr,
    nets: *mut Joint,
    transport: *mut Joint,
    rules: *mut RuleTable,
}

/// The classifiers of the rules of any family: the protocol and the port join
/// into the transport, decoded into the rule map.
struct AnyFamily {
    proto: *mut LineAttr,
    port: *mut PortAttr,
    transport: *mut Joint,
    rules: *mut RuleMap,
}

/// Every classifier of the tests' rules, in a C arena of its own.
struct Classifiers {
    arena: Arena,
    v4: Family<Net4Attr>,
    v6: Family<Net6Attr>,
    any: AnyFamily,
}

/// Composes the classifier of a family over the rules whose networks are of
/// the family, leaving a rule of another family out of every projection.
fn family<N: Attribute>(
    arena: &Arena,
    rules: &[(&Lines<'_>, &Ports<'_>)],
    nets: &[Option<&(N, N)>],
) -> Family<N::Classifier> {
    let src = nets.iter().map(|pair| pair.map(|(src, _)| src)).collect::<Vec<_>>();
    let dst = nets.iter().map(|pair| pair.map(|(_, dst)| dst)).collect::<Vec<_>>();
    let protos = rules
        .iter()
        .zip(nets)
        .map(|(&(lines, _), pair)| pair.map(|_| lines))
        .collect::<Vec<_>>();
    let ports = rules
        .iter()
        .zip(nets)
        .map(|(&(_, ports), pair)| pair.map(|_| ports))
        .collect::<Vec<_>>();
    let (src, src_stage) = compile(arena, c"src", &src);
    let (dst, dst_stage) = compile(arena, c"dst", &dst);
    let (proto, proto_stage) = compile(arena, c"proto", &protos);
    let (port, port_stage) = compile(arena, c"port", &ports);
    let (nets_joint, nets_stage) = join(arena, c"nets", (src_stage, dst_stage), count(nets));
    let (transport, transport_stage) = join(arena, c"transport", (proto_stage, port_stage), count(nets));
    Family {
        src,
        dst,
        proto,
        port,
        nets: nets_joint,
        transport,
        rules: join_rules(arena, c"rules", (nets_stage, transport_stage), nets),
    }
}

impl Classifiers {
    fn build(rules: &[TestRule]) -> Self {
        let arena = Arena::new();
        let lines = rules
            .iter()
            .map(|rule| Lines::new(&rule.protos).unwrap())
            .collect::<Vec<_>>();
        let ports = rules
            .iter()
            .map(|rule| Ports::new(&rule.ports).unwrap())
            .collect::<Vec<_>>();
        let transport = lines.iter().zip(&ports).collect::<Vec<_>>();
        let nets4 = rules
            .iter()
            .map(|rule| match &rule.nets {
                Nets::V4(src, dst) => Some((Nets4::new(src).unwrap(), Nets4::new(dst).unwrap())),
                _ => None,
            })
            .collect::<Vec<_>>();
        let nets6 = rules
            .iter()
            .map(|rule| match &rule.nets {
                Nets::V6(src, dst) => Some((Nets6::new(src).unwrap(), Nets6::new(dst).unwrap())),
                _ => None,
            })
            .collect::<Vec<_>>();
        let v4 = family(
            &arena,
            &transport,
            &nets4.iter().map(Option::as_ref).collect::<Vec<_>>(),
        );
        let v6 = family(
            &arena,
            &transport,
            &nets6.iter().map(Option::as_ref).collect::<Vec<_>>(),
        );

        let any = rules
            .iter()
            .map(|rule| matches!(rule.nets, Nets::Any).then_some(()))
            .collect::<Vec<_>>();
        let any = any.iter().map(Option::as_ref).collect::<Vec<_>>();
        let protos = lines
            .iter()
            .zip(&any)
            .map(|(lines, slot)| slot.map(|_| lines))
            .collect::<Vec<_>>();
        let any_ports = ports
            .iter()
            .zip(&any)
            .map(|(ports, slot)| slot.map(|_| ports))
            .collect::<Vec<_>>();
        let (proto, proto_stage) = compile(&arena, c"any proto", &protos);
        let (port, port_stage) = compile(&arena, c"any port", &any_ports);
        let (transport, transport_stage) = join(&arena, c"any transport", (proto_stage, port_stage), count(&any));
        let any = AnyFamily {
            proto,
            port,
            transport,
            rules: decode(&arena, c"any rules", transport_stage, &any),
        };
        Self { arena, v4, v6, any }
    }
}

/// Asserts that the outputs of the Rust lookup of a stage equal those of the
/// C one, naming the first input they differ at.
#[track_caller]
fn assert_same<T: PartialEq + Debug>(stage: &str, c: &[T], rust: &[T]) {
    assert_eq!(c.len(), rust.len(), "{stage}: outputs of different lengths");
    if let Some(idx) = c.iter().zip(rust).position(|(c, rust)| c != rust) {
        panic!(
            "{stage}: input {idx} reads {:?} in C and {:?} in Rust",
            c[idx], rust[idx]
        );
    }
}

/// The IPv4 addresses at and around the edges of every IPv4 network of the
/// rules, with the first and the last of the domain.
fn edges4(rules: &[TestRule]) -> Vec<Addr4> {
    let mut addrs = vec![0, u32::MAX];
    for rule in rules {
        if let Nets::V4(src, dst) = &rule.nets {
            for net in src.iter().chain(dst) {
                let (addr, mask) = (u32::from_be_bytes(net.addr), u32::from_be_bytes(net.mask));
                let (first, last) = (addr & mask, addr | !mask);
                addrs.extend([
                    first.wrapping_sub(1),
                    first,
                    first + (last - first) / 2,
                    last,
                    last.wrapping_add(1),
                ]);
            }
        }
    }
    addrs.into_iter().map(|addr| Addr4::from(addr.to_be_bytes())).collect()
}

/// The IPv6 addresses at and around the edges of every IPv6 network of the
/// rules, with the first and the last of the domain.
fn edges6(rules: &[TestRule]) -> Vec<[u8; 16]> {
    let mut addrs = vec![0, u128::MAX];
    for rule in rules {
        if let Nets::V6(src, dst) = &rule.nets {
            for net in src.iter().chain(dst) {
                let (addr, mask) = (u128::from_be_bytes(net.addr), u128::from_be_bytes(net.mask));
                let (first, last) = (addr & mask, addr | !mask);
                addrs.extend([
                    first.wrapping_sub(1),
                    first,
                    first + (last - first) / 2,
                    last,
                    last.wrapping_add(1),
                ]);
            }
        }
    }
    addrs.into_iter().map(u128::to_be_bytes).collect()
}

/// Every pair of classes of a table of the counts, one past each end and the
/// largest class on either side.
fn pairs(rows: u32, columns: u32) -> (Vec<u32>, Vec<u32>) {
    let mut pairs = (Vec::new(), Vec::new());
    for row in (0..=rows).chain([u32::MAX]) {
        for column in (0..=columns).chain([u32::MAX]) {
            pairs.0.push(row);
            pairs.1.push(column);
        }
    }
    pairs
}

/// Compares the IPv4 classifier both ways over the addresses.
fn check_net4(cx: &Arena, stage: &str, attr: *mut Net4Attr, addrs: &[Addr4]) {
    let (mut c, mut rust) = (vec![0; addrs.len()], vec![0; addrs.len()]);
    // SAFETY: a built classifier; an address is its word.
    unsafe {
        bindings::yanet_sys_classify_net4_lookup(attr.cast(), addrs.as_ptr().cast(), c.as_mut_ptr(), count(addrs))
    };
    // SAFETY: as above.
    unsafe { &*attr }.lookup(cx, addrs, &mut rust);
    assert_same(stage, &c, &rust);
}

/// Compares the IPv6 classifier both ways over the addresses.
fn check_net6(cx: &Arena, stage: &str, attr: *mut Net6Attr, addrs: &[[u8; 16]]) {
    let (mut c, mut rust) = (vec![0; addrs.len()], vec![0; addrs.len()]);
    // SAFETY: a built classifier; the addresses are consecutive bytes.
    unsafe {
        bindings::yanet_sys_classify_net6_lookup(attr.cast(), addrs.as_ptr().cast(), c.as_mut_ptr(), count(addrs))
    };
    // SAFETY: as above.
    unsafe { &*attr }.lookup(cx, addrs, &mut rust);
    assert_same(stage, &c, &rust);
}

/// Compares the port classifier both ways over every port.
fn check_port(cx: &Arena, stage: &str, attr: *mut PortAttr) {
    let ports = (0..=u16::MAX).collect::<Vec<_>>();
    let (mut c, mut rust) = (vec![0; ports.len()], vec![0; ports.len()]);
    // SAFETY: a built classifier.
    unsafe { bindings::yanet_sys_classify_port_lookup(attr.cast(), ports.as_ptr(), c.as_mut_ptr(), count(&ports)) };
    // SAFETY: as above.
    unsafe { &*attr }.lookup(cx, &ports, &mut rust);
    assert_same(stage, &c, &rust);
}

/// Compares the byte classifier both ways over every byte.
fn check_line(cx: &Arena, stage: &str, attr: *mut LineAttr) {
    let bytes = (0..=u8::MAX).collect::<Vec<_>>();
    let (mut c, mut rust) = (vec![0; bytes.len()], vec![0; bytes.len()]);
    // SAFETY: a built classifier.
    unsafe { bindings::yanet_sys_classify_line_lookup(attr.cast(), bytes.as_ptr(), c.as_mut_ptr(), count(&bytes)) };
    // SAFETY: as above.
    unsafe { &*attr }.lookup(cx, &bytes, &mut rust);
    assert_same(stage, &c, &rust);
}

/// Compares the joint both ways over every pair of classes of its stages and
/// past them.
fn check_joint(cx: &Arena, stage: &str, joint: *mut Joint) {
    // SAFETY: a built joint, read only.
    let (rows, columns) = unsafe {
        let table = &*joint.cast::<bindings::value_table>();
        (table.v_dim, table.h_dim)
    };
    let (left, right) = pairs(rows, columns);
    let (mut c, mut rust) = (vec![0; left.len()], vec![0; left.len()]);
    // SAFETY: as above.
    unsafe {
        bindings::yanet_sys_classify_joint_lookup(
            joint.cast(),
            left.as_ptr(),
            right.as_ptr(),
            c.as_mut_ptr(),
            count(&left),
        )
    };
    // SAFETY: as above.
    unsafe { &*joint }.lookup(cx, &left, &right, &mut rust);
    assert_same(stage, &c, &rust);
}

/// Compares the rule table both ways over every pair of classes of its stages
/// and past them.
fn check_rule_table(cx: &Arena, stage: &str, table: *mut RuleTable) {
    // SAFETY: a built table, read only.
    let (rows, columns) = unsafe {
        let table = &*table.cast::<bindings::rule_table>();
        (table.v_dim, table.h_dim)
    };
    let (left, right) = pairs(rows, columns);
    let (mut c, mut rust) = (vec![Rule::NONE; left.len()], vec![Rule::NONE; left.len()]);
    // SAFETY: as above; a rule is its word.
    unsafe {
        bindings::yanet_sys_classify_rule_table_lookup(
            table.cast(),
            left.as_ptr(),
            right.as_ptr(),
            c.as_mut_ptr().cast(),
            count(&left),
        )
    };
    // SAFETY: as above.
    unsafe { &*table }.lookup(cx, &left, &right, &mut rust);
    assert_same(stage, &c, &rust);
}

/// verifies that the Rust lookups of the IPv4, IPv6, port and byte
/// classifiers of both families read the classes the C ones do.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_attribute_lookups_match_the_shim() {
    let rules = rules();
    let Classifiers { arena, v4, v6, any } = Classifiers::build(&rules);
    // SAFETY: a built classifier, read only. The rules split the low halves
    // of one IPv6 high half, so the destination classifier has a table to
    // read.
    assert_ne!(0, unsafe {
        (*v6.dst.cast::<bindings::classify_attr_net6>()).comb.v_dim
    });

    let (addrs4, addrs6) = (edges4(&rules), edges6(&rules));
    check_net4(&arena, "v4 src", v4.src, &addrs4);
    check_net4(&arena, "v4 dst", v4.dst, &addrs4);
    check_net6(&arena, "v6 src", v6.src, &addrs6);
    check_net6(&arena, "v6 dst", v6.dst, &addrs6);
    for (stage, attr) in [("v4 port", v4.port), ("v6 port", v6.port), ("any port", any.port)] {
        check_port(&arena, stage, attr);
    }
    for (stage, attr) in [("v4 proto", v4.proto), ("v6 proto", v6.proto), ("any proto", any.proto)] {
        check_line(&arena, stage, attr);
    }
}

/// verifies that the Rust lookups of the joints of both families read the
/// classes the C ones do, class zero past a stage included.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_joint_lookups_match_the_shim() {
    let Classifiers { arena, v4, v6, any } = Classifiers::build(&rules());
    for (stage, joint) in [
        ("v4 nets", v4.nets),
        ("v4 transport", v4.transport),
        ("v6 nets", v6.nets),
        ("v6 transport", v6.transport),
        ("any transport", any.transport),
    ] {
        check_joint(&arena, stage, joint);
    }
}

/// verifies that the Rust lookups of the rule tables and the rule map read
/// the rules the C ones do, no rule past a stage included.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_rule_lookups_match_the_shim() {
    let Classifiers { arena, v4, v6, any } = Classifiers::build(&rules());
    check_rule_table(&arena, "v4 rules", v4.rules);
    check_rule_table(&arena, "v6 rules", v6.rules);

    // SAFETY: a built map, read only.
    let size = unsafe { (*any.rules.cast::<bindings::vline>()).size };
    let classes = (0..=size).chain([u32::MAX]).collect::<Vec<_>>();
    let (mut c, mut rust) = (vec![Rule::NONE; classes.len()], vec![Rule::NONE; classes.len()]);
    // SAFETY: as above; a rule is its word.
    unsafe {
        bindings::yanet_sys_classify_resolve(
            any.rules.cast(),
            classes.as_ptr(),
            c.as_mut_ptr().cast(),
            count(&classes),
        )
    };
    // SAFETY: as above.
    unsafe { &*any.rules }.lookup(&arena, &classes, &mut rust);
    assert_same("any rules", &c, &rust);
}

/// verifies that the Rust merge of two decodings keeps the rule the C one
/// does for every pair of rules, no rule on either side included.
#[test]
#[cfg_attr(miri, ignore = "Miri does not run C")]
fn test_results_min_matches_the_shim() {
    // SAFETY: a rule is a transparent word, and every word a rule, the
    // largest none.
    let rules = [0, 1, 7, u32::MAX - 1, u32::MAX].map(|word| unsafe { transmute::<u32, Rule>(word) });
    let (first, second): (Vec<_>, Vec<_>) = rules
        .iter()
        .flat_map(|&rule| rules.iter().map(move |&other| (rule, other)))
        .unzip();
    let (mut c, mut rust) = (first.clone(), first);
    // SAFETY: both runs are of one length; a rule is its word.
    unsafe { bindings::yanet_sys_classify_results_min(c.as_mut_ptr().cast(), second.as_ptr().cast(), count(&second)) };
    results_min(&mut rust, &second);
    assert_same("results min", &c, &rust);
}
