//! A module that assigns each packet to the first rule it matches, by its
//! networks, its protocol and its destination port: the classify bindings
//! wired through as a module composes them.
//!
//! Each family composes its own classifier tree over the rules of the
//! family: the source and the destination networks join, the protocol and the
//! port join, and the two joints meet in the rule table. The rules of any
//! family, which have no networks, compose apart: the protocol and the port
//! join and are decoded into a rule map, and a packet takes the earlier of the
//! rules of both trees.

use yanet_sdk::{
    Addr4, Batch, CommitCx, Counter, Cx, Decode, Join, LineAttr, Module, Net4Attr, Net6Attr, PortAttr, Rules, ShmBox,
    ShmLayout, ShmSlice, Traffic, Verdict, results_min,
};

/// The protocol and the destination port of a packet, joined.
pub type Transport = Join<LineAttr, PortAttr>;

/// The classifier of the IPv4 rules: the source and the destination
/// networks joined, against the transport.
pub type Assign4 = Rules<Join<Net4Attr, Net4Attr>, Transport>;

/// The classifier of the IPv6 rules, shaped as the IPv4 one.
pub type Assign6 = Rules<Join<Net6Attr, Net6Attr>, Transport>;

/// The classifier of the rules of any family, by protocol and port only.
pub type AssignAny = Decode<Transport>;

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
pub struct Assign {
    pub v4: ShmBox<Assign4>,
    pub v6: ShmBox<Assign6>,
    pub any: ShmBox<AssignAny>,
    pub actions: ShmSlice<Action>,
}

/// Passes or drops each IP packet by the action of the first rule it
/// matches, and drops a packet no rule matches and one that is not IP.
///
/// A packet without a usable transport header, a fragment after the first
/// among them, classifies as of port zero.
impl Module for Assign {
    const NAME: &'static str = "assign";
    type Prepared<'p> = ();

    fn handle<'id>(&'id self, cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let actions = self.actions.as_slice(cx);
        batch.dispatch(|packet| {
            let (data, network) = (packet.data(), usize::from(packet.network_offset()));
            let port = (packet.transport_header_available() && matches!(packet.transport_protocol(), 6 | 17))
                .then(|| bytes(data, usize::from(packet.transport_offset()) + 2))
                .flatten()
                .map_or(0, u16::from_be_bytes);
            let proto = packet.transport_protocol();
            let family = match packet.network_type() {
                0x0800 => match (bytes(data, network + 12), bytes(data, network + 16)) {
                    (Some(src), Some(dst)) => {
                        let nets = (Addr4::from(src), Addr4::from(dst));
                        Some(self.v4.get(cx).lookup(cx, (nets, (proto, port))))
                    }
                    _ => None,
                },
                0x86dd => match (bytes(data, network + 8), bytes(data, network + 24)) {
                    (Some(src), Some(dst)) => Some(self.v6.get(cx).lookup(cx, ((src, dst), (proto, port)))),
                    _ => None,
                },
                _ => None,
            };
            let Some(family) = family else {
                return Verdict::Drop;
            };
            let mut rule = [family];
            results_min(&mut rule, &[self.any.get(cx).lookup(cx, (proto, port))]);
            let [rule] = rule;
            let Some(action) = rule.index().and_then(|rule| actions.get(rule)) else {
                return Verdict::Drop;
            };
            action.counter.add(cx, Traffic::packet(data.len() as u64));
            if action.pass != 0 { Verdict::Pass } else { Verdict::Drop }
        });
    }

    fn prepare<'p>(&'p self, _: &CommitCx<'p, Self>) {}
}

/// The `N` bytes of the frame at the offset, if it holds them.
fn bytes<const N: usize>(data: &[u8], offset: usize) -> Option<[u8; N]> {
    data.get(offset..offset.checked_add(N)?)?.try_into().ok()
}
