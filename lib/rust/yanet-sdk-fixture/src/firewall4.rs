//! An authored firewall assembled from a statically shaped classifier graph.

#![forbid(unsafe_code)]

use yanet_sdk::{
    Batch, CommitCx, CounterPair, Cx, Module, ShmLayout, ShmSlice, Verdict,
    classify::{Decode, Graph, Ipv4Key, Joint, Net4},
    packet::Ipv4Packet,
};

pub const SOURCE: usize = 0;
pub const DESTINATION: usize = 1;
pub const NETWORKS: usize = 2;
pub const FIRST_RULE: usize = 3;

pub type Ipv4Graph = Graph<(Net4, Net4, Joint<SOURCE, DESTINATION>, Decode<NETWORKS>)>;

#[derive(ShmLayout)]
#[repr(C)]
pub struct Target {
    pub counter: CounterPair,
    pub actions: [u8; 8],
    pub action_count: u64,
}

#[derive(ShmLayout)]
#[repr(C)]
pub struct Config {
    pub ipv4: Ipv4Graph,
    pub targets: ShmSlice<Target>,
}

pub struct Firewall4;

impl Module for Firewall4 {
    const NAME: &'static str = "firewall4";
    type Config = Config;
    type Prepared<'p> = ();

    fn prepare(_: &CommitCx<'_, Self>) {}

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let config = cx.config();
        while let Some(matches) = config.ipv4.with_query::<64, _>(cx, batch, |query| {
            let source = query
                .node::<SOURCE>()
                .net4(|packet| ipv4_keys(packet.data(), packet.network_offset()).map_or(Ipv4Key::ZERO, |keys| keys[0]));
            let destination = query
                .node::<DESTINATION>()
                .net4(|packet| ipv4_keys(packet.data(), packet.network_offset()).map_or(Ipv4Key::ZERO, |keys| keys[1]));
            let networks = query.node::<NETWORKS>().join(source, destination);
            query.node::<FIRST_RULE>().decode(networks)
        }) {
            matches.dispatch(|packet, rule| {
                if packet.network_type() != 0x0800 || ipv4_keys(packet.data(), packet.network_offset()).is_none() {
                    return Verdict::Drop;
                }
                let Some(target) = rule.and_then(|index| config.targets.as_slice(cx).get(index)) else {
                    return Verdict::Drop;
                };
                for action in target.actions.iter().take(target.action_count as usize) {
                    match action {
                        0 => return Verdict::Pass,
                        1 => return Verdict::Drop,
                        2 => target.counter.add(cx, 1, packet.data().len() as u64),
                        _ => return Verdict::Drop,
                    }
                }
                Verdict::Drop
            });
        }
    }
}

/// Extracts aligned address keys from a complete checked IPv4 packet.
fn ipv4_keys(data: &[u8], offset: u16) -> Option<[Ipv4Key; 2]> {
    let packet = Ipv4Packet::parse(data.get(usize::from(offset)..)?).ok()?;
    Some([
        Ipv4Key::new(packet.header.src().octets()),
        Ipv4Key::new(packet.header.dst().octets()),
    ])
}
