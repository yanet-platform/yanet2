//! A Rust firewall composed from native classification primitives.
#![forbid(unsafe_code)]
use yanet_sdk::{
    Batch, CommitCx, CounterPair, Cx, Module, PacketView, ShmLayout, ShmSlice, Verdict,
    classify::{ByteLine, Decode, Device, Graph, IpFrag, Ipv4Key, Joint, Net4, Net6, Port, RuleTable},
};
pub type FirewallGraph = Graph<(
    Device,
    Decode<0>,
    Device,
    Net4,
    Net4,
    ByteLine,
    Joint<3, 4>,
    Joint<2, 6>,
    Joint<7, 5>,
    IpFrag,
    Joint<8, 9>,
    Decode<10>,
    Port,
    Port,
    Joint<12, 13>,
    ByteLine,
    Joint<14, 15>,
    RuleTable<8, 16>,
    RuleTable<8, 14>,
    ByteLine,
    RuleTable<8, 19>,
    RuleTable<8, 15>,
    Decode<8>,
    Device,
    Net6,
    Net6,
    ByteLine,
    Joint<24, 25>,
    Joint<23, 27>,
    Joint<28, 26>,
    IpFrag,
    Joint<29, 30>,
    Decode<31>,
    Port,
    Port,
    Joint<33, 34>,
    ByteLine,
    Joint<35, 36>,
    RuleTable<29, 37>,
    RuleTable<29, 35>,
    ByteLine,
    RuleTable<29, 40>,
    RuleTable<29, 36>,
    Decode<29>,
)>;
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
    pub graph: FirewallGraph,
    pub targets: ShmSlice<Target>,
}
pub struct Firewall;
impl Module for Firewall {
    const NAME: &'static str = "firewall";
    type Config = Config;
    type Prepared<'p> = ();
    fn prepare(_: &CommitCx<'_, Self>) {}
    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let config = cx.config();
        while let Some(matches) = config.graph.with_query::<64, _>(cx, batch, |q| {
            let l2 = q.node::<1>().decode_device(q.node::<0>().device());
            let source = q.node::<3>().net4(|p| Ipv4Key::new(address4(p, 12)));
            let destination = q.node::<4>().net4(|p| Ipv4Key::new(address4(p, 16)));
            let networks = q.node::<6>().join(source, destination);
            let middle = q.node::<7>().join_device(q.node::<2>().device(), networks);
            let protocol = q.node::<5>().byte(|p| p.transport_type() as u8);
            let core = q.node::<8>().join(middle, protocol);
            let frag = q.node::<9>().fragment(|p| p.fragment_offset() > 0);
            let plain = q.node::<11>().decode(q.node::<10>().join(core, frag));
            let sport = q.node::<12>().port(|p| port(p, 0));
            let dport = q.node::<13>().port(|p| port(p, 2));
            let ports = q.node::<14>().join(sport, dport);
            let flags = q.node::<15>().byte(|p| transport_byte(p, 13));
            let tcp_suffix = q.node::<16>().join(ports, flags);
            let tcp = q
                .node::<17>()
                .rules(core, tcp_suffix)
                .first(q.node::<21>().rules(core, flags));
            let udp = q.node::<18>().rules(core, ports).first(q.node::<22>().decode(core));
            let icmp_type = q.node::<19>().byte(|p| transport_byte(p, 0));
            let icmp = q.node::<20>().rules(core, icmp_type);
            let transport = q
                .select(tcp, |p| p.fragment_offset() == 0 && p.transport_type() as u8 == 6)
                .first(q.select(udp, |p| p.fragment_offset() == 0 && p.transport_type() as u8 == 17))
                .first(q.select(icmp, |p| {
                    p.fragment_offset() == 0 && matches!(p.transport_type() as u8, 1 | 58)
                }));
            let family2 = q.select(plain.first(transport), |p| p.network_type() == 0x0800);
            let source = q.node::<24>().net6(|p| address6(p, 8));
            let destination = q.node::<25>().net6(|p| address6(p, 24));
            let networks = q.node::<27>().join(source, destination);
            let middle = q.node::<28>().join_device(q.node::<23>().device(), networks);
            let protocol = q.node::<26>().byte(|p| p.transport_type() as u8);
            let core = q.node::<29>().join(middle, protocol);
            let frag = q.node::<30>().fragment(|p| p.fragment_offset() > 0);
            let plain = q.node::<32>().decode(q.node::<31>().join(core, frag));
            let sport = q.node::<33>().port(|p| port(p, 0));
            let dport = q.node::<34>().port(|p| port(p, 2));
            let ports = q.node::<35>().join(sport, dport);
            let flags = q.node::<36>().byte(|p| transport_byte(p, 13));
            let tcp_suffix = q.node::<37>().join(ports, flags);
            let tcp = q
                .node::<38>()
                .rules(core, tcp_suffix)
                .first(q.node::<42>().rules(core, flags));
            let udp = q.node::<39>().rules(core, ports).first(q.node::<43>().decode(core));
            let icmp_type = q.node::<40>().byte(|p| transport_byte(p, 0));
            let icmp = q.node::<41>().rules(core, icmp_type);
            let transport = q
                .select(tcp, |p| p.fragment_offset() == 0 && p.transport_type() as u8 == 6)
                .first(q.select(udp, |p| p.fragment_offset() == 0 && p.transport_type() as u8 == 17))
                .first(q.select(icmp, |p| {
                    p.fragment_offset() == 0 && matches!(p.transport_type() as u8, 1 | 58)
                }));
            let family23 = q.select(plain.first(transport), |p| p.network_type() == 0x86dd);
            l2.first(family2).first(family23)
        }) {
            matches.dispatch(|packet, rule| {
                let Some(target) = rule.and_then(|i| config.targets.as_slice(cx).get(i)) else {
                    return Verdict::Drop;
                };
                for action in target.actions.iter().take(target.action_count as usize) {
                    match action {
                        0 => return Verdict::Pass,
                        1 => return Verdict::Drop,
                        2 => target.counter.add(cx, 1, packet.data().len() as u64),
                        3..=5 => {}
                        _ => return Verdict::Drop,
                    }
                }
                Verdict::Drop
            });
        }
    }
}
fn address4(p: PacketView<'_>, offset: usize) -> [u8; 4] {
    p.data()
        .get(usize::from(p.network_offset()) + offset..)
        .and_then(|s| s.get(..4))
        .and_then(|s| s.try_into().ok())
        .unwrap_or([0; 4])
}
fn address6(p: PacketView<'_>, offset: usize) -> [u8; 16] {
    p.data()
        .get(usize::from(p.network_offset()) + offset..)
        .and_then(|s| s.get(..16))
        .and_then(|s| s.try_into().ok())
        .unwrap_or([0; 16])
}
fn transport_byte(p: PacketView<'_>, offset: usize) -> u8 {
    p.data()
        .get(usize::from(p.transport_offset()) + offset)
        .copied()
        .unwrap_or(0)
}
fn port(p: PacketView<'_>, offset: usize) -> u16 {
    if !matches!(p.transport_type(), 6 | 17) {
        return 0;
    }
    let start = usize::from(p.transport_offset());
    p.data()
        .get(start..start + 4)
        .and_then(|s| s.get(offset..offset + 2))
        .and_then(|s| s.try_into().ok())
        .map(u16::from_be_bytes)
        .unwrap_or(0)
}
