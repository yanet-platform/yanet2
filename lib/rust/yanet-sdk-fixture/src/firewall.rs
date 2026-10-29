//! The firewall module: the classifier of its rules, composed as the C ACL
//! module composes its own, and the actions of the rule each packet matches
//! first, applied as the C ACL handler applies them.

use yanet_sdk::{
    Batch, CommitCx, CounterPair, Cx, DeviceAttr, FragAttr, Joint, LineAttr, Module, NO_RULE, Net4Attr, Net6Attr,
    PacketView, PortAttr, RuleMap, RuleTable, ShmBox, ShmLayout, ShmSlice, results_min,
};

// The action kinds of a rule, those of the C ACL rules. The names are prefixed
// because the api crate's C header carries them as macros.

/// Passes the packet.
pub const FIREWALL_ACTION_ALLOW: u8 = 0;
/// Drops the packet.
pub const FIREWALL_ACTION_DENY: u8 = 1;
/// Counts the packet and its bytes in the rule's counter, and goes on.
pub const FIREWALL_ACTION_COUNT: u8 = 2;
/// Goes on: the firewall keeps no state table, so no state is found, as for a
/// C ACL without a state map.
pub const FIREWALL_ACTION_CHECK_STATE: u8 = 3;
/// Goes on: the firewall keeps no state table to sync.
pub const FIREWALL_ACTION_CREATE_STATE: u8 = 4;
/// Goes on: the fixture logs nothing.
pub const FIREWALL_ACTION_LOG: u8 = 5;

/// The most actions of a rule, as the C ACL target holds them.
pub const FIREWALL_MAX_ACTIONS: usize = 8;

/// The configuration of [`Firewall`]: the classifier of its rules, composed
/// as the C ACL module composes its own, and what each rule does.
///
/// The rules without networks match every packet of their devices through the
/// device classes alone; each family filters through its own composition.
#[derive(ShmLayout)]
#[repr(C)]
pub struct FirewallConfig {
    pub l2_device: ShmBox<DeviceAttr>,
    pub l2_rules: ShmBox<RuleMap>,
    pub ip4: Ip4,
    pub ip6: Ip6,
    pub targets: ShmSlice<Target>,
}

/// The IPv4 classifiers: the network pair and the family composition.
#[derive(ShmLayout)]
#[repr(C)]
pub struct Ip4 {
    pub src: ShmBox<Net4Attr>,
    pub dst: ShmBox<Net4Attr>,
    pub family: Family,
}

/// The IPv6 classifiers, as the IPv4 ones.
#[derive(ShmLayout)]
#[repr(C)]
pub struct Ip6 {
    pub src: ShmBox<Net6Attr>,
    pub dst: ShmBox<Net6Attr>,
    pub family: Family,
}

/// The classification of a family after its networks.
///
/// The core joins the network pair, the device and the protocol number. The
/// plain filter joins the fragment classes onto the core and decodes the rules
/// that match without a transport header; each protocol path joins its own
/// classes onto the core in a rule table, the TCP and the UDP ones through the
/// shared port pair, and the rules whose ports match any port through a table
/// or a map of their own.
#[derive(ShmLayout)]
#[repr(C)]
pub struct Family {
    pub device: ShmBox<DeviceAttr>,
    pub proto: ShmBox<LineAttr>,
    pub nets: ShmBox<Joint>,
    pub mid: ShmBox<Joint>,
    pub core: ShmBox<Joint>,
    pub frag: ShmBox<FragAttr>,
    pub plain: ShmBox<Joint>,
    pub plain_rules: ShmBox<RuleMap>,
    pub src_port: ShmBox<PortAttr>,
    pub dst_port: ShmBox<PortAttr>,
    pub ports: ShmBox<Joint>,
    pub tcp_flags: ShmBox<LineAttr>,
    pub flags: ShmBox<Joint>,
    pub icmp_type: ShmBox<LineAttr>,
    pub tcp: ShmBox<RuleTable>,
    pub tcp_any_port: ShmBox<RuleTable>,
    pub udp: ShmBox<RuleTable>,
    pub udp_any_port: ShmBox<RuleMap>,
    pub icmp: ShmBox<RuleTable>,
}

/// What a rule does with the packets it matches: the first `action_count` of
/// its `FIREWALL_ACTION_*` kinds, in order, and the counter they count in.
#[derive(ShmLayout)]
#[repr(C)]
pub struct Target {
    pub counter: CounterPair,
    pub actions: [u8; FIREWALL_MAX_ACTIONS],
    pub action_count: u64,
}

impl Target {
    /// Applies the actions to a packet of that many bytes, in order, and
    /// reports whether the packet passes.
    ///
    /// An allow passes and a deny drops, a count adds the packet to the
    /// counter, and the state kinds and a log go on to the next action, as
    /// they do in a C ACL without a state map; an unknown kind drops, and so
    /// does a list without an allow or a deny.
    fn allows<'id>(&'id self, cx: &Cx<'id, Firewall>, bytes: u64) -> bool {
        let count = usize::try_from(self.action_count).unwrap_or(usize::MAX);
        for &action in self.actions.iter().take(count) {
            match action {
                FIREWALL_ACTION_ALLOW => return true,
                FIREWALL_ACTION_DENY => return false,
                FIREWALL_ACTION_COUNT => self.counter.add(cx, 1, bytes),
                FIREWALL_ACTION_CHECK_STATE | FIREWALL_ACTION_CREATE_STATE | FIREWALL_ACTION_LOG => {}
                _ => return false,
            }
        }
        false
    }
}

/// Applies to a packet the actions of the first rule it matches, as the C
/// ACL module does, and drops it when no rule matches.
pub struct Firewall;

impl Module for Firewall {
    const NAME: &'static str = "firewall";
    type Config = FirewallConfig;
    type Prepared<'p> = ();

    fn handle<'id>(cx: &mut Cx<'id, Self>, batch: &mut Batch<'id>) {
        let config = cx.config();
        let targets = config.targets.as_slice(cx);
        let mut ip4 = Packets::default();
        let mut ip6 = Packets::default();
        loop {
            let mut rules = [NO_RULE; BATCH];
            let count = classify(cx, config, batch, (&mut ip4, &mut ip6), &mut rules);
            if count == 0 {
                return;
            }
            for &rule in &rules[..count] {
                let Some(packet) = batch.next() else {
                    return;
                };
                // A packet that matches no rule classifies past the targets.
                let bytes = packet.data().len() as u64;
                if usize::try_from(rule)
                    .ok()
                    .and_then(|rule| targets.get(rule))
                    .is_some_and(|target| target.allows(cx, bytes))
                {
                    batch.pass(packet);
                } else {
                    batch.drop(packet);
                }
            }
        }
    }

    fn prepare(_: &CommitCx<'_, Self>) {}
}

/// The most packets one classification takes, as the C ACL handler batches
/// them.
const BATCH: usize = 64;

const ETHER_IPV4: u16 = 0x0800;
const ETHER_IPV6: u16 = 0x86dd;
const PROTO_ICMP: u16 = 1;
const PROTO_TCP: u16 = 6;
const PROTO_UDP: u16 = 17;
const PROTO_ICMPV6: u16 = 58;

/// Classifies the input packets the handler has not taken, up to [`BATCH`],
/// and writes the first rule each matches in their order, [`NO_RULE`] for
/// none; returns how many it classified.
///
/// The first match of a packet is the first of those of the rules without
/// networks, of its family's plain filter and of its protocol path. The
/// packets of each family are gathered into scratch the handler reuses.
fn classify<'id>(
    cx: &Cx<'id, Firewall>,
    config: &'id FirewallConfig,
    batch: &Batch<'id>,
    (ip4, ip6): (&mut Packets<u32>, &mut Packets<[u8; 16]>),
    rules: &mut [u32; BATCH],
) -> usize {
    ip4.clear();
    ip6.clear();
    let mut count = 0;
    for packet in batch.peek().take(BATCH) {
        // A batch holds at most a byte's worth of places.
        let at = count as u8;
        match packet.network_type() {
            ETHER_IPV4 => ip4.push(at, &packet, |header| (word(header, 12), word(header, 16))),
            ETHER_IPV6 => ip6.push(at, &packet, |header| (addr6(header, 8), addr6(header, 24))),
            _ => {}
        }
        count += 1;
    }

    let device = config.l2_device.get(cx).lookup(cx);
    let mut l2 = [NO_RULE];
    config.l2_rules.get(cx).lookup(cx, &[device], &mut l2);
    rules[..count].fill(l2[0]);

    let (mut src, mut dst) = ([0; BATCH], [0; BATCH]);
    config.ip4.src.get(cx).lookup(cx, ip4.src(), &mut src);
    config.ip4.dst.get(cx).lookup(cx, ip4.dst(), &mut dst);
    ip4.classify(cx, &config.ip4.family, &src, &dst, rules);
    config.ip6.src.get(cx).lookup(cx, ip6.src(), &mut src);
    config.ip6.dst.get(cx).lookup(cx, ip6.dst(), &mut dst);
    ip6.classify(cx, &config.ip6.family, &src, &dst, rules);
    count
}

/// The four bytes of a header at an offset as a word in memory order, zero
/// past the head segment.
fn word(header: &[u8], offset: usize) -> u32 {
    header
        .get(offset..offset + 4)
        .and_then(|bytes| bytes.try_into().ok())
        .map_or(0, u32::from_ne_bytes)
}

/// The IPv6 address at an offset of a header, zero past the head segment.
fn addr6(header: &[u8], offset: usize) -> [u8; 16] {
    header
        .get(offset..offset + 16)
        .and_then(|bytes| bytes.try_into().ok())
        .unwrap_or_default()
}

/// The packets of one family of a batch with what the classification reads
/// of them: the addresses, the protocol and whether they are fragments, and
/// the packets of each protocol path.
struct Packets<A> {
    len: usize,
    /// Where each packet is in the batch.
    at: [u8; BATCH],
    src: [A; BATCH],
    dst: [A; BATCH],
    proto: [u8; BATCH],
    fragment: [bool; BATCH],
    tcp: Path,
    udp: Path,
    icmp: Path,
}

impl<A: Copy + Default> Default for Packets<A> {
    fn default() -> Self {
        Self {
            len: 0,
            at: [0; BATCH],
            src: [A::default(); BATCH],
            dst: [A::default(); BATCH],
            proto: [0; BATCH],
            fragment: [false; BATCH],
            tcp: Path::default(),
            udp: Path::default(),
            icmp: Path::default(),
        }
    }
}

/// The packets of a family a protocol path classifies: the whole ones of the
/// path's protocol, with their ports and the byte the path reads (the TCP
/// flags, the ICMP type).
struct Path {
    len: usize,
    /// Where each packet is in its family.
    at: [u8; BATCH],
    src_port: [u16; BATCH],
    dst_port: [u16; BATCH],
    byte: [u8; BATCH],
}

impl Default for Path {
    fn default() -> Self {
        Self {
            len: 0,
            at: [0; BATCH],
            src_port: [0; BATCH],
            dst_port: [0; BATCH],
            byte: [0; BATCH],
        }
    }
}

impl Path {
    fn push(&mut self, at: u8, (src_port, dst_port): (u16, u16), byte: u8) {
        let idx = self.len;
        self.len += 1;
        self.at[idx] = at;
        self.src_port[idx] = src_port;
        self.dst_port[idx] = dst_port;
        self.byte[idx] = byte;
    }

    /// The core classes of the path's packets, out of those of the family.
    fn core(&self, family: &[u32; BATCH]) -> [u32; BATCH] {
        let mut core = [0; BATCH];
        for (class, &at) in core.iter_mut().zip(&self.at[..self.len]) {
            *class = family[usize::from(at)];
        }
        core
    }

    /// The classes of the port pair of the path's packets.
    fn ports<'id>(&self, cx: &Cx<'id, Firewall>, family: &'id Family) -> [u32; BATCH] {
        let len = self.len;
        let (mut src, mut dst, mut ports) = ([0; BATCH], [0; BATCH], [0; BATCH]);
        family.src_port.get(cx).lookup(cx, &self.src_port[..len], &mut src);
        family.dst_port.get(cx).lookup(cx, &self.dst_port[..len], &mut dst);
        family.ports.get(cx).lookup(cx, &src[..len], &dst[..len], &mut ports);
        ports
    }
}

impl<A: Copy> Packets<A> {
    /// Empties the scratch for the next batch.
    fn clear(&mut self) {
        self.len = 0;
        self.tcp.len = 0;
        self.udp.len = 0;
        self.icmp.len = 0;
    }

    /// Adds a packet of the family at a place of the batch, whose addresses
    /// the function reads out of its network header.
    fn push(&mut self, at: u8, packet: &PacketView<'_>, addrs: impl Fn(&[u8]) -> (A, A)) {
        let idx = self.len;
        self.len += 1;
        self.at[idx] = at;
        let data = packet.data();
        let network = data.get(usize::from(packet.network_offset())..).unwrap_or_default();
        (self.src[idx], self.dst[idx]) = addrs(network);
        // The low byte is the protocol of the IP header, of a later
        // fragment too.
        self.proto[idx] = packet.transport_type() as u8;
        self.fragment[idx] = packet.fragment_offset() != 0;
        if self.fragment[idx] {
            return;
        }
        let transport = data.get(usize::from(packet.transport_offset())..).unwrap_or_default();
        let ports = || {
            let ports = word(transport, 0).to_ne_bytes();
            (
                u16::from_be_bytes([ports[0], ports[1]]),
                u16::from_be_bytes([ports[2], ports[3]]),
            )
        };
        // A family holds at most a batch.
        let idx = idx as u8;
        match packet.transport_type() {
            PROTO_TCP => self.tcp.push(idx, ports(), transport.get(13).copied().unwrap_or(0)),
            PROTO_UDP => self.udp.push(idx, ports(), 0),
            PROTO_ICMP | PROTO_ICMPV6 => self.icmp.push(idx, (0, 0), transport.first().copied().unwrap_or(0)),
            _ => {}
        }
    }

    fn src(&self) -> &[A] {
        &self.src[..self.len]
    }

    fn dst(&self) -> &[A] {
        &self.dst[..self.len]
    }

    /// Classifies the family's packets by the classes of their source and
    /// destination networks, keeping in each rule of the batch the first
    /// match.
    fn classify<'id>(
        &self,
        cx: &Cx<'id, Firewall>,
        family: &'id Family,
        src: &[u32; BATCH],
        dst: &[u32; BATCH],
        rules: &mut [u32; BATCH],
    ) {
        let len = self.len;
        if len == 0 {
            return;
        }
        let device = family.device.get(cx).lookup(cx);
        let (mut proto, mut nets, mut mid, mut core) = ([0; BATCH], [0; BATCH], [0; BATCH], [0; BATCH]);
        family.proto.get(cx).lookup(cx, &self.proto[..len], &mut proto);
        family.nets.get(cx).lookup(cx, &src[..len], &dst[..len], &mut nets);
        family.mid.get(cx).lookup_const(cx, device, &nets[..len], &mut mid);
        family.core.get(cx).lookup(cx, &mid[..len], &proto[..len], &mut core);

        let (mut frag, mut plain, mut found) = ([0; BATCH], [0; BATCH], [NO_RULE; BATCH]);
        family.frag.get(cx).lookup(cx, &self.fragment[..len], &mut frag);
        family.plain.get(cx).lookup(cx, &core[..len], &frag[..len], &mut plain);
        family.plain_rules.get(cx).lookup(cx, &plain[..len], &mut found);
        for (&at, &rule) in self.at[..len].iter().zip(&found) {
            let at = usize::from(at);
            rules[at] = rules[at].min(rule);
        }

        self.merge(&self.tcp, self.tcp(cx, family, &core), rules);
        self.merge(&self.udp, self.udp(cx, family, &core), rules);
        self.merge(&self.icmp, self.icmp(cx, family, &core), rules);
    }

    /// Keeps in each rule of the batch the first match of a path's packet.
    fn merge(&self, path: &Path, found: [u32; BATCH], rules: &mut [u32; BATCH]) {
        for (&at, &rule) in path.at[..path.len].iter().zip(&found) {
            let at = usize::from(self.at[usize::from(at)]);
            rules[at] = rules[at].min(rule);
        }
    }

    /// The first rules of the TCP path: by the port pair and the flags, and
    /// by the flags alone for the rules of any port.
    fn tcp<'id>(&self, cx: &Cx<'id, Firewall>, family: &'id Family, core: &[u32; BATCH]) -> [u32; BATCH] {
        let path = &self.tcp;
        let len = path.len;
        let core = path.core(core);
        let ports = path.ports(cx, family);
        let (mut flags, mut mid, mut found, mut any_port) =
            ([0; BATCH], [0; BATCH], [NO_RULE; BATCH], [NO_RULE; BATCH]);
        family.tcp_flags.get(cx).lookup(cx, &path.byte[..len], &mut flags);
        family.flags.get(cx).lookup(cx, &ports[..len], &flags[..len], &mut mid);
        family.tcp.get(cx).lookup(cx, &core[..len], &mid[..len], &mut found);
        family
            .tcp_any_port
            .get(cx)
            .lookup(cx, &core[..len], &flags[..len], &mut any_port);
        results_min(&mut found[..len], &any_port);
        found
    }

    /// The first rules of the UDP path: by the port pair, and by the core
    /// classes alone for the rules of any port.
    fn udp<'id>(&self, cx: &Cx<'id, Firewall>, family: &'id Family, core: &[u32; BATCH]) -> [u32; BATCH] {
        let path = &self.udp;
        let len = path.len;
        let core = path.core(core);
        let ports = path.ports(cx, family);
        let (mut found, mut any_port) = ([NO_RULE; BATCH], [NO_RULE; BATCH]);
        family.udp.get(cx).lookup(cx, &core[..len], &ports[..len], &mut found);
        family.udp_any_port.get(cx).lookup(cx, &core[..len], &mut any_port);
        results_min(&mut found[..len], &any_port);
        found
    }

    /// The first rules of the ICMP path, by the message type.
    fn icmp<'id>(&self, cx: &Cx<'id, Firewall>, family: &'id Family, core: &[u32; BATCH]) -> [u32; BATCH] {
        let path = &self.icmp;
        let len = path.len;
        let core = path.core(core);
        let (mut types, mut found) = ([0; BATCH], [NO_RULE; BATCH]);
        family.icmp_type.get(cx).lookup(cx, &path.byte[..len], &mut types);
        family.icmp.get(cx).lookup(cx, &core[..len], &types[..len], &mut found);
        found
    }
}
