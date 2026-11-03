//! A module that assigns each packet to the first rule it matches, by its
//! networks, its protocol and its destination port: the classify bindings
//! wired through as a module composes them.
//!
//! Each family composes its own classifier over the rules of the family: the
//! source and the destination networks join into one stage, the protocol and
//! the port into another, and the two into the rule table. The rules of any
//! family, which have no networks, compose apart: the protocol and the port
//! join into one stage, decoded into a rule map, and a packet takes the earlier
//! of the rules of both paths.

use yanet_sdk::{
    Batch, CommitCx, Counter, Cx, Joint, LineAttr, Module, NO_RULE, Net4Attr, Net6Attr, PortAttr, RuleMap, RuleTable,
    ShmBox, ShmLayout, ShmSlice, Traffic, Verdict, results_min,
};

/// The classifier of the IPv4 rules.
#[derive(ShmLayout)]
#[repr(C)]
pub struct Assign4 {
    pub src: ShmBox<Net4Attr>,
    pub dst: ShmBox<Net4Attr>,
    pub proto: ShmBox<LineAttr>,
    pub port: ShmBox<PortAttr>,
    pub nets: ShmBox<Joint>,
    pub transport: ShmBox<Joint>,
    pub rules: ShmBox<RuleTable>,
}

/// The classifier of the IPv6 rules.
#[derive(ShmLayout)]
#[repr(C)]
pub struct Assign6 {
    pub src: ShmBox<Net6Attr>,
    pub dst: ShmBox<Net6Attr>,
    pub proto: ShmBox<LineAttr>,
    pub port: ShmBox<PortAttr>,
    pub nets: ShmBox<Joint>,
    pub transport: ShmBox<Joint>,
    pub rules: ShmBox<RuleTable>,
}

/// The classifier of the rules of any family, by protocol and port only.
#[derive(ShmLayout)]
#[repr(C)]
pub struct AssignAny {
    pub proto: ShmBox<LineAttr>,
    pub port: ShmBox<PortAttr>,
    pub transport: ShmBox<Joint>,
    pub rules: ShmBox<RuleMap>,
}

/// What a rule does with the packets it is the first match of: passes them
/// when `pass` is not zero, drops them otherwise, and counts them.
#[derive(ShmLayout)]
#[repr(C)]
pub struct Action {
    pub pass: u64,
    pub counter: Counter<Traffic>,
}

/// The configuration of [`Assign`]: a classifier per family, one of the rules
/// of any family and an action per rule, by the rule's index.
#[derive(ShmLayout)]
#[repr(C)]
pub struct AssignConfig {
    pub v4: Assign4,
    pub v6: Assign6,
    pub any: AssignAny,
    pub actions: ShmSlice<Action>,
}

/// Passes or drops each IP packet by the action of the first rule it
/// matches, and drops a packet no rule matches and one that is not IP.
///
/// A packet without a usable transport header, a fragment after the first
/// among them, classifies as of port zero.
pub struct Assign;

impl Module for Assign {
    const NAME: &'static str = "assign";
    type Config = AssignConfig;
    type Prepared<'p> = ();

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let config = cx.config();
        let actions = config.actions.as_slice(cx);
        batch.dispatch(|packet| {
            let (data, network) = (packet.data(), usize::from(packet.network_offset()));
            let port = (packet.transport_header_available() && matches!(packet.transport_protocol(), 6 | 17))
                .then(|| bytes(data, usize::from(packet.transport_offset()) + 2))
                .flatten()
                .map_or(0, u16::from_be_bytes);
            let proto = packet.transport_protocol();
            let family = match packet.network_type() {
                0x0800 => match (bytes(data, network + 12), bytes(data, network + 16)) {
                    (Some(src), Some(dst)) => Some(assign4(cx, &config.v4, src, dst, proto, port)),
                    _ => None,
                },
                0x86dd => match (bytes(data, network + 8), bytes(data, network + 24)) {
                    (Some(src), Some(dst)) => Some(assign6(cx, &config.v6, src, dst, proto, port)),
                    _ => None,
                },
                _ => None,
            };
            let Some(family) = family else {
                return Verdict::Drop;
            };
            let mut rule = [family];
            results_min(&mut rule, &[assign_any(cx, &config.any, proto, port)]);
            let [rule] = rule;
            let Some(action) = usize::try_from(rule).ok().and_then(|rule| actions.get(rule)) else {
                return Verdict::Drop;
            };
            action.counter.add(cx, Traffic::packet(data.len() as u64));
            if action.pass != 0 { Verdict::Pass } else { Verdict::Drop }
        });
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}

/// The `N` bytes of the frame at the offset, if it holds them.
fn bytes<const N: usize>(data: &[u8], offset: usize) -> Option<[u8; N]> {
    data.get(offset..offset.checked_add(N)?)?.try_into().ok()
}

/// The first rule of any family the packet matches, [`NO_RULE`] for none.
fn assign_any<'id>(cx: &Cx<'id, Assign>, any: &'id AssignAny, proto: u8, port: u16) -> u32 {
    let (mut proto_class, mut port_class, mut transport_class) = ([0], [0], [0]);
    any.proto.get(cx).lookup(cx, &[proto], &mut proto_class);
    any.port.get(cx).lookup(cx, &[port], &mut port_class);
    any.transport
        .get(cx)
        .lookup(cx, &proto_class, &port_class, &mut transport_class);
    let mut rule = [NO_RULE];
    any.rules.get(cx).lookup(cx, &transport_class, &mut rule);
    rule[0]
}

/// The first IPv4 rule the packet matches, [`NO_RULE`] for none.
fn assign4<'id>(cx: &Cx<'id, Assign>, family: &'id Assign4, src: [u8; 4], dst: [u8; 4], proto: u8, port: u16) -> u32 {
    let (mut src_class, mut dst_class) = ([0], [0]);
    family
        .src
        .get(cx)
        .lookup(cx, &[u32::from_ne_bytes(src)], &mut src_class);
    family
        .dst
        .get(cx)
        .lookup(cx, &[u32::from_ne_bytes(dst)], &mut dst_class);
    rule(
        cx,
        &family.nets,
        &family.transport,
        &family.rules,
        (src_class, dst_class),
        (&family.proto, &family.port),
        proto,
        port,
    )
}

/// The first IPv6 rule the packet matches, [`NO_RULE`] for none.
fn assign6<'id>(cx: &Cx<'id, Assign>, family: &'id Assign6, src: [u8; 16], dst: [u8; 16], proto: u8, port: u16) -> u32 {
    let (mut src_class, mut dst_class) = ([0], [0]);
    family.src.get(cx).lookup(cx, &[src], &mut src_class);
    family.dst.get(cx).lookup(cx, &[dst], &mut dst_class);
    rule(
        cx,
        &family.nets,
        &family.transport,
        &family.rules,
        (src_class, dst_class),
        (&family.proto, &family.port),
        proto,
        port,
    )
}

/// The rule of a packet whose network classes are known: the transport
/// classes, then both joints, then the rule table.
#[allow(clippy::too_many_arguments)]
fn rule<'id>(
    cx: &Cx<'id, Assign>,
    nets: &'id ShmBox<Joint>,
    transport: &'id ShmBox<Joint>,
    rules: &'id ShmBox<RuleTable>,
    (src_class, dst_class): ([u32; 1], [u32; 1]),
    (proto_attr, port_attr): (&'id ShmBox<LineAttr>, &'id ShmBox<PortAttr>),
    proto: u8,
    port: u16,
) -> u32 {
    let (mut proto_class, mut port_class) = ([0], [0]);
    proto_attr.get(cx).lookup(cx, &[proto], &mut proto_class);
    port_attr.get(cx).lookup(cx, &[port], &mut port_class);
    let (mut nets_class, mut transport_class) = ([0], [0]);
    nets.get(cx).lookup(cx, &src_class, &dst_class, &mut nets_class);
    transport
        .get(cx)
        .lookup(cx, &proto_class, &port_class, &mut transport_class);
    let mut rule = [NO_RULE];
    rules.get(cx).lookup(cx, &nets_class, &transport_class, &mut rule);
    rule[0]
}
