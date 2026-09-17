//! Renders ACL rules as ipfw-style `add` lines.

use core::{
    fmt::{self, Display, Formatter},
    net::{Ipv4Addr, Ipv6Addr},
};

use commonpb::pb::{IPv4Network, IPv6Network};
use filterpb::pb::{FragmentKind, IpNet, PortRange, ProtoRange, VlanRange};
use ync::output::{Paint, Painted};

use crate::aclpb::{ActionKind, Rule};

/// The colour role of one token of a rule line.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Tone {
    Plain,
    Keyword,
    Pass,
    Deny,
    Option,
    Muted,
}

impl Tone {
    fn paint(self) -> Option<Paint> {
        match self {
            Self::Plain => None,
            Self::Keyword | Self::Muted => Some(Paint::Dim),
            Self::Pass => Some(Paint::SoftOk),
            Self::Deny => Some(Paint::SoftError),
            Self::Option => Some(Paint::Warning),
        }
    }
}

/// One rule, written as its `add` line on display.
pub struct RuleLine<'a> {
    rule: &'a Rule,
    colored: bool,
    /// The first pass or deny action. The actions after it never run.
    terminal: Option<usize>,
    /// An unknown kind reads as the unrestricted one, as the service refuses
    /// it on update.
    fragment: FragmentKind,
    has4: bool,
    has6: bool,
    layer2: bool,
    /// The rule matches no packet and is dimmed as a whole.
    dead: bool,
}

impl<'a> RuleLine<'a> {
    pub fn new(rule: &'a Rule, colored: bool) -> Self {
        let terminal = rule
            .actions
            .iter()
            .position(|action| action.kind == ActionKind::Pass as i32 || action.kind == ActionKind::Deny as i32);

        let (srcs, dsts) = legacy_networks(rule);
        let src = Side::new(&rule.sources4, &rule.sources6, srcs, true, true);
        let dst = Side::new(&rule.destinations4, &rule.destinations6, dsts, true, true);
        let (src4, src6) = (src.nets().any(|net| net.is_v4()), src.nets().any(|net| !net.is_v4()));
        let (dst4, dst6) = (dst.nets().any(|net| net.is_v4()), dst.nets().any(|net| !net.is_v4()));

        let has4 = src4 && dst4;
        let has6 = src6 && dst6;
        let layer2 = !(src4 || src6 || dst4 || dst6);
        let fragment = rule.fragment.as_ref().map_or(FragmentKind::Any, |fragment| {
            FragmentKind::try_from(fragment.kind).unwrap_or(FragmentKind::Any)
        });
        let no_protocols = !protocols_matchable(&rule.proto_ranges);
        // A fragment rule leaves port matching, so its ports restrict nothing.
        let ports =
            fragment != FragmentKind::Frag && (has_ports(&rule.src_port_ranges) || has_ports(&rule.dst_port_ranges));
        let no_vlans = !rule.vlan_ranges.is_empty() && rule.vlan_ranges.iter().all(|r| r.from > r.to);
        let dead = no_vlans
            || (!layer2 && (!(has4 || has6) || no_protocols || (ports && !protocols_with_ports(&rule.proto_ranges))));

        Self {
            rule,
            colored,
            terminal,
            fragment,
            has4,
            has6,
            layer2,
            dead,
        }
    }

    /// Returns whether the rule reaches a count action, the only one that
    /// moves its counter.
    pub fn counts(&self) -> bool {
        self.rule
            .actions
            .iter()
            .enumerate()
            .any(|(idx, action)| action.kind == ActionKind::Count as i32 && self.reachable(idx))
    }

    fn reachable(&self, idx: usize) -> bool {
        self.terminal.is_none_or(|terminal| idx < terminal)
    }

    fn write_options(&self, out: &mut Tokens) -> Result<(), fmt::Error> {
        for (idx, action) in self.rule.actions.iter().enumerate() {
            if Some(idx) == self.terminal {
                continue;
            }

            let tone = if self.reachable(idx) { Tone::Option } else { Tone::Muted };
            out.token(tone, ActionName(action.kind))?;

            if action.kind == ActionKind::Count as i32 && !self.rule.counter.is_empty() {
                out.token(tone, Quoted(&self.rule.counter))?;
            }
        }

        Ok(())
    }

    fn write_devices(&self, out: &mut Tokens) -> Result<(), fmt::Error> {
        let names = || {
            self.rule
                .devices
                .iter()
                .map(|device| device.name.as_str())
                .filter(|name| !name.is_empty())
        };

        match names().count() {
            0 => Ok(()),
            1 => {
                out.token(Tone::Keyword, "via")?;
                names().try_for_each(|name| out.token(Tone::Plain, name))
            }
            _ => {
                out.token(Tone::Keyword, "{")?;
                for (idx, name) in names().enumerate() {
                    if idx > 0 {
                        out.token(Tone::Keyword, "or")?;
                    }
                    out.token(Tone::Keyword, "via")?;
                    out.token(Tone::Plain, name)?;
                }
                out.token(Tone::Keyword, "}")
            }
        }
    }
}

impl Display for RuleLine<'_> {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let rule = self.rule;
        let mut out = Tokens {
            f,
            colored: self.colored,
            dead: self.dead,
            first: true,
        };

        out.token(Tone::Keyword, "add")?;
        match self.terminal.map(|idx| rule.actions[idx].kind) {
            Some(kind) if kind == ActionKind::Pass as i32 => out.token(Tone::Pass, "pass")?,
            Some(..) => out.token(Tone::Deny, "deny")?,
            None => out.token(Tone::Muted, "deny")?,
        }

        if self.layer2 {
            out.token(Tone::Keyword, "layer2")?;
            self.write_devices(&mut out)?;
            write_vlans(&mut out, &rule.vlan_ranges)?;
            return self.write_options(&mut out);
        }

        // A dead rule shows every network, a live one only the families it
        // matches.
        let with4 = self.has4 || self.dead;
        let with6 = self.has6 || self.dead;
        let (srcs, dsts) = legacy_networks(rule);
        let src = Side::new(&rule.sources4, &rule.sources6, srcs, with4, with6);
        let dst = Side::new(&rule.destinations4, &rule.destinations6, dsts, with4, with6);

        // A single-family rule open on both sides would read as matching
        // both families, so the family is spelled out.
        let family = match (!self.dead && src.is_any() && dst.is_any(), self.has4, self.has6) {
            (true, true, false) => Some("ip4"),
            (true, false, true) => Some("ip6"),
            _ => None,
        };

        let protocols = Protocols::new(&rule.proto_ranges);
        let class = protocols.class();
        match family {
            Some(family) if class == Class::All => out.token(Tone::Plain, family)?,
            _ => protocols.write_slot(class, &mut out)?,
        }

        // A later fragment carries no transport header, so the ports and the
        // header conditions of a fragment rule match on payload bytes.
        let ports = if self.fragment == FragmentKind::Frag {
            Tone::Muted
        } else {
            Tone::Plain
        };

        out.token(Tone::Keyword, "from")?;
        src.write(&mut out)?;
        write_ports(&mut out, &rule.src_port_ranges, ports)?;
        out.token(Tone::Keyword, "to")?;
        dst.write(&mut out)?;
        write_ports(&mut out, &rule.dst_port_ranges, ports)?;
        self.write_devices(&mut out)?;
        write_vlans(&mut out, &rule.vlan_ranges)?;
        match self.fragment {
            FragmentKind::Frag => out.token(Tone::Plain, "frag")?,
            FragmentKind::None => {
                out.token(Tone::Keyword, "not")?;
                out.token(Tone::Plain, "frag")?;
            }
            FragmentKind::Any => {}
        }
        if let Some(family) = family.filter(|_| class != Class::All) {
            out.token(Tone::Keyword, family)?;
        }
        protocols.write_options(class, &mut out, ports)?;
        self.write_options(&mut out)
    }
}

/// Writes the tokens of one line separated by spaces, each in the colour of
/// its tone.
struct Tokens<'a, 'b> {
    f: &'a mut Formatter<'b>,
    colored: bool,
    dead: bool,
    first: bool,
}

impl Tokens<'_, '_> {
    /// Returns a comma-separated list for a token of the given tone, commas
    /// dimmed like keywords unless the whole token or line is painted.
    fn list<I>(&self, items: I, tone: Tone) -> List<I> {
        let paint = (self.colored && !self.dead && tone.paint().is_none()).then_some(Paint::Dim);

        List {
            items,
            comma: Painted::new(paint, ","),
        }
    }

    fn token(&mut self, tone: Tone, value: impl Display) -> Result<(), fmt::Error> {
        if !self.first {
            self.f.write_str(" ")?;
        }
        self.first = false;

        let paint = if self.dead { Some(Paint::Dim) } else { tone.paint() };

        write!(self.f, "{}", Painted::new(paint.filter(|_| self.colored), value))
    }
}

/// Returns whether the ranges hold a protocol value a packet can carry.
///
/// A packet builds the value from its protocol and, for TCP, ICMP and
/// ICMPv6, the flags or type byte, every other protocol carrying a zero
/// byte.
fn protocols_matchable(ranges: &[ProtoRange]) -> bool {
    protocols_any(ranges, |proto, low, high| match proto {
        1 | 6 | 58 => low <= high,
        _ => low == 0,
    })
}

/// Returns whether the ranges hold TCP or UDP, the protocols the port
/// matching sees.
fn protocols_with_ports(ranges: &[ProtoRange]) -> bool {
    protocols_any(ranges, |proto, low, high| match proto {
        6 => low <= high,
        17 => low == 0,
        _ => false,
    })
}

/// Returns whether any protocol of the ranges holds bytes the test accepts.
fn protocols_any(ranges: &[ProtoRange], test: impl Fn(u32, u32, u32) -> bool) -> bool {
    ranges.iter().any(|r| {
        if r.from > 0xffff || r.from > r.to {
            return false;
        }
        let (from, to) = (r.from, r.to.min(0xffff));

        ((from >> 8)..=(to >> 8)).any(|proto| {
            let low = if proto == from >> 8 { from & 0xff } else { 0 };
            let high = if proto == to >> 8 { to & 0xff } else { 0xff };

            test(proto, low, high)
        })
    })
}

/// Returns whether the ranges restrict the ports, which the dataplane reads
/// from the first one alone.
fn has_ports(ranges: &[PortRange]) -> bool {
    ranges.first().is_some_and(|r| !(r.from == 0 && r.to >= 65535))
}

/// Writes port ranges, nothing when the first one covers every port, which
/// is how the dataplane leaves a rule out of port matching.
fn write_ports(out: &mut Tokens, ranges: &[PortRange], tone: Tone) -> Result<(), fmt::Error> {
    if !has_ports(ranges) {
        return Ok(());
    }

    let list = out.list(ranges.iter().map(|r| Range(r.from, r.to)), tone);
    out.token(tone, list)
}

/// Writes VLAN ranges, nothing when they cover every VLAN.
fn write_vlans(out: &mut Tokens, ranges: &[VlanRange]) -> Result<(), fmt::Error> {
    if ranges.is_empty() || ranges.iter().any(|r| r.from == 0 && r.to >= 4095) {
        return Ok(());
    }

    out.token(Tone::Keyword, "vlan")?;
    let list = out.list(ranges.iter().map(|r| Range(r.from, r.to)), Tone::Plain);
    out.token(Tone::Plain, list)
}

/// Items written comma-separated.
struct List<I> {
    items: I,
    comma: Painted<&'static str>,
}

impl<I> Display for List<I>
where
    I: Iterator + Clone,
    I::Item: Display,
{
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        for (idx, item) in self.items.clone().enumerate() {
            if idx > 0 {
                write!(f, "{}", self.comma)?;
            }
            write!(f, "{item}")?;
        }

        Ok(())
    }
}

/// An inclusive range, a single value when both ends meet.
struct Range(u32, u32);

impl Display for Range {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let Self(from, to) = *self;

        if from == to {
            write!(f, "{from}")
        } else {
            write!(f, "{from}-{to}")
        }
    }
}

/// Text in double quotes, its quotes, backslashes and control characters
/// escaped.
struct Quoted<'a>(&'a str);

impl Display for Quoted<'_> {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        write!(f, "\"{}\"", self.0.escape_debug())
    }
}

/// The name of an action kind as an option, an undeclared kind by number.
struct ActionName(i32);

impl Display for ActionName {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let name = match ActionKind::try_from(self.0) {
            Ok(ActionKind::Pass) => "pass",
            Ok(ActionKind::Deny) => "deny",
            Ok(ActionKind::Count) => "count",
            Ok(ActionKind::CheckState) => "check-state",
            Ok(ActionKind::CreateState) => "keep-state",
            Ok(ActionKind::Log) => "log",
            Err(..) => return write!(f, "action({})", self.0),
        };

        f.write_str(name)
    }
}

/// Returns the deprecated mixed-family sources and destinations, which a
/// config written through them shows back.
#[allow(deprecated)]
fn legacy_networks(rule: &Rule) -> (&[IpNet], &[IpNet]) {
    (&rule.srcs, &rule.dsts)
}

/// One side of a rule, `from` or `to`, limited to the families shown.
#[derive(Clone, Copy)]
struct Side<'a> {
    nets4: &'a [IPv4Network],
    nets6: &'a [IPv6Network],
    legacy: &'a [IpNet],
    with4: bool,
    with6: bool,
}

impl<'a> Side<'a> {
    fn new(nets4: &'a [IPv4Network], nets6: &'a [IPv6Network], legacy: &'a [IpNet], with4: bool, with6: bool) -> Self {
        Self { nets4, nets6, legacy, with4, with6 }
    }

    /// Returns the networks of the shown families: typed IPv4, deprecated
    /// IPv4, typed IPv6, deprecated IPv6.
    fn nets(&self) -> impl Iterator<Item = Net> + Clone + 'a {
        let Self { nets4, nets6, legacy, with4, with6 } = *self;
        let legacy = legacy.iter().filter_map(Net::legacy);

        nets4
            .iter()
            .map(Net::v4)
            .chain(legacy.clone().filter(|net| net.is_v4()))
            .filter(move |_| with4)
            .chain(
                nets6
                    .iter()
                    .map(Net::v6)
                    .chain(legacy.filter(|net| !net.is_v4()))
                    .filter(move |_| with6),
            )
    }

    /// Returns whether every shown family holds a zero-mask network.
    fn is_any(&self) -> bool {
        let full4 = self.nets().any(|net| net.is_v4() && net.is_full());
        let full6 = self.nets().any(|net| !net.is_v4() && net.is_full());

        (!self.with4 || full4) && (!self.with6 || full6)
    }

    fn write(&self, out: &mut Tokens) -> Result<(), fmt::Error> {
        if self.is_any() {
            out.token(Tone::Keyword, "any")
        } else if self.nets().next().is_none() {
            out.token(Tone::Keyword, "none")
        } else {
            let list = out.list(self.nets(), Tone::Plain);
            out.token(Tone::Plain, list)
        }
    }
}

/// A network by address and mask.
#[derive(Debug, Clone, Copy)]
enum Net {
    V4(Ipv4Addr, Ipv4Addr),
    V6(Ipv6Addr, Ipv6Addr),
}

impl Net {
    fn v4(net: &IPv4Network) -> Self {
        Self::V4(
            net.addr.as_ref().map_or(Ipv4Addr::UNSPECIFIED, Ipv4Addr::from),
            net.mask.as_ref().map_or(Ipv4Addr::BROADCAST, Ipv4Addr::from),
        )
    }

    fn v6(net: &IPv6Network) -> Self {
        Self::V6(
            net.addr.as_ref().map_or(Ipv6Addr::UNSPECIFIED, Ipv6Addr::from),
            net.mask.as_ref().map_or(Ipv6Addr::from(u128::MAX), Ipv6Addr::from),
        )
    }

    /// Reads a deprecated network, `None` for a malformed one.
    fn legacy(net: &IpNet) -> Option<Self> {
        let (addr, mask) = (net.addr.as_slice(), net.mask.as_slice());

        if let (Ok(addr), Ok(mask)) = (<[u8; 4]>::try_from(addr), <[u8; 4]>::try_from(mask)) {
            return Some(Self::V4(addr.into(), mask.into()));
        }
        if let (Ok(addr), Ok(mask)) = (<[u8; 16]>::try_from(addr), <[u8; 16]>::try_from(mask)) {
            return Some(Self::V6(addr.into(), mask.into()));
        }

        None
    }

    fn is_v4(&self) -> bool {
        matches!(self, Self::V4(..))
    }

    fn is_full(&self) -> bool {
        match self {
            Self::V4(_, mask) => mask.is_unspecified(),
            Self::V6(_, mask) => mask.is_unspecified(),
        }
    }
}

impl Display for Net {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match *self {
            Self::V4(addr, mask) => {
                let bits = u32::from(mask);
                let ones = bits.leading_ones();
                match (ones, bits.checked_shl(ones).unwrap_or(0)) {
                    (32, _) => write!(f, "{addr}"),
                    (_, 0) => write!(f, "{addr}/{ones}"),
                    _ => write!(f, "{addr}/{mask}"),
                }
            }
            Self::V6(addr, mask) => {
                let bits = u128::from(mask);
                let ones = bits.leading_ones();
                match (ones, bits.checked_shl(ones).unwrap_or(0)) {
                    (128, _) => write!(f, "{addr}"),
                    (_, 0) => write!(f, "{addr}/{ones}"),
                    _ => write!(f, "{addr}/{mask}"),
                }
            }
        }
    }
}

/// How the protocol ranges of a rule read.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Class {
    /// No protocol, the rule never matches.
    None,
    /// Every protocol with every flag byte.
    All,
    /// Whole protocols by name.
    Names,
    /// TCP limited by flags ipfw can spell.
    Tcp,
    /// ICMP limited by types.
    Icmp,
    /// ICMPv6 limited by types.
    Icmp6,
    /// Anything else, as the encoded ranges.
    Raw,
}

/// Protocol ranges as a set of low bytes per protocol, a value encoding
/// `protocol << 8 | byte` with TCP flags or an ICMP type in the byte.
struct Protocols {
    bytes: [[u64; 4]; 256],
}

impl Protocols {
    fn new(ranges: &[ProtoRange]) -> Self {
        let mut bytes = [[0u64; 4]; 256];

        for r in ranges {
            if r.from > 0xffff || r.from > r.to {
                continue;
            }
            let (from, to) = (r.from, r.to.min(0xffff));

            for proto in (from >> 8)..=(to >> 8) {
                let low = if proto == from >> 8 { from & 0xff } else { 0 };
                let high = if proto == to >> 8 { to & 0xff } else { 0xff };
                let set = &mut bytes[proto as usize];

                if low == 0 && high == 0xff {
                    *set = [u64::MAX; 4];
                    continue;
                }
                for byte in low..=high {
                    set[(byte / 64) as usize] |= 1 << (byte % 64);
                }
            }
        }

        Self { bytes }
    }

    fn set(&self, proto: u8) -> &[u64; 4] {
        &self.bytes[usize::from(proto)]
    }

    fn present(&self) -> impl Iterator<Item = u8> + Clone + '_ {
        (0..=255u8).filter(|proto| *self.set(*proto) != [0; 4])
    }

    fn class(&self) -> Class {
        let full = |proto: u8| *self.set(proto) == [u64::MAX; 4];

        let mut present = self.present();
        let Some(first) = present.next() else {
            return Class::None;
        };
        if self.bytes.iter().all(|set| *set == [u64::MAX; 4]) {
            return Class::All;
        }
        if self.present().all(full) {
            return Class::Names;
        }

        match (first, present.next()) {
            (6, None) if TcpMatch::new(self.set(6)).is_some() => Class::Tcp,
            (1, None) => Class::Icmp,
            (58, None) => Class::Icmp6,
            _ => Class::Raw,
        }
    }

    fn write_slot(&self, class: Class, out: &mut Tokens) -> Result<(), fmt::Error> {
        match class {
            Class::None => out.token(Tone::Plain, "none"),
            Class::All => out.token(Tone::Plain, "ip"),
            Class::Names if self.present().nth(1).is_some() => {
                out.token(Tone::Keyword, "{")?;
                for (idx, proto) in self.present().enumerate() {
                    if idx > 0 {
                        out.token(Tone::Keyword, "or")?;
                    }
                    out.token(Tone::Plain, ProtoName(proto))?;
                }
                out.token(Tone::Keyword, "}")
            }
            Class::Names | Class::Tcp | Class::Icmp | Class::Icmp6 => self
                .present()
                .try_for_each(|proto| out.token(Tone::Plain, ProtoName(proto))),
            Class::Raw => {
                out.token(Tone::Keyword, "proto-range")?;
                out.token(Tone::Plain, RawRanges(self))
            }
        }
    }

    fn write_options(&self, class: Class, out: &mut Tokens, tone: Tone) -> Result<(), fmt::Error> {
        match class {
            Class::Tcp => write_tcp_flags(self.set(6), out, tone),
            Class::Icmp => {
                out.token(Tone::Keyword, "icmptypes")?;
                let list = out.list(byte_set(self.set(1)), tone);
                out.token(tone, list)
            }
            Class::Icmp6 => {
                out.token(Tone::Keyword, "icmp6types")?;
                let list = out.list(byte_set(self.set(58)), tone);
                out.token(tone, list)
            }
            Class::None | Class::All | Class::Names | Class::Raw => Ok(()),
        }
    }
}

/// The protocol set as merged encoded ranges.
struct RawRanges<'a>(&'a Protocols);

impl Display for RawRanges<'_> {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let mut start = None;
        let mut first = true;

        for value in 0..=0x10000u32 {
            let set = value <= 0xffff && {
                let set = &self.0.bytes[(value >> 8) as usize];
                let byte = value & 0xff;
                set[(byte / 64) as usize] & (1 << (byte % 64)) != 0
            };

            match (set, start) {
                (true, None) => start = Some(value),
                (false, Some(from)) => {
                    if !first {
                        f.write_str(",")?;
                    }
                    first = false;
                    write!(f, "{}", Range(from, value - 1))?;
                    start = None;
                }
                _ => {}
            }
        }

        Ok(())
    }
}

/// A protocol by its ipfw name, or by number.
struct ProtoName(u8);

impl Display for ProtoName {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let name = match self.0 {
            1 => "icmp",
            2 => "igmp",
            4 => "ipencap",
            6 => "tcp",
            17 => "udp",
            41 => "ipv6",
            47 => "gre",
            50 => "esp",
            51 => "ah",
            58 => "ipv6-icmp",
            89 => "ospf",
            103 => "pim",
            112 => "vrrp",
            132 => "sctp",
            proto => return write!(f, "{proto}"),
        };

        f.write_str(name)
    }
}

fn byte_set(set: &[u64; 4]) -> impl Iterator<Item = u8> + Clone + '_ {
    (0..=255u8).filter(|byte| set[usize::from(byte / 64)] & (1 << (byte % 64)) != 0)
}

/// TCP flag names by bit, lowest first, as ipfw spells them.
const TCP_FLAGS: [&str; 8] = ["fin", "syn", "rst", "psh", "ack", "urg", "ece", "cwr"];

/// The RST and ACK bits of `established`.
const ESTABLISHED: u8 = 0x14;

/// A set of TCP flag bytes as ipfw can spell it.
#[derive(Debug, Clone, Copy)]
enum TcpMatch {
    /// `tcpflags`: some bits must be set and some clear.
    Flags { value: u8, care: u8 },
    /// `established`: RST or ACK is set.
    Established,
}

impl TcpMatch {
    /// Reads the set, `None` when ipfw has no spelling for it.
    fn new(set: &[u64; 4]) -> Option<Self> {
        let contains = |byte: u8| set[usize::from(byte / 64)] & (1 << (byte % 64)) != 0;

        if (0..=255u8).all(|byte| contains(byte) == (byte & ESTABLISHED != 0)) {
            return Some(Self::Established);
        }

        // The set is one cube when it holds two to the power of the bits its
        // bytes do not share.
        let (ones, zeros) = byte_set(set).fold((u8::MAX, u8::MAX), |(ones, zeros), byte| (ones & byte, zeros & !byte));
        let care = ones | zeros;
        let size: u32 = set.iter().map(|word| word.count_ones()).sum();

        (1u32 << care.count_zeros() == size).then_some(Self::Flags { value: ones, care })
    }
}

fn write_tcp_flags(set: &[u64; 4], out: &mut Tokens, tone: Tone) -> Result<(), fmt::Error> {
    match TcpMatch::new(set) {
        Some(TcpMatch::Flags { value, care }) => {
            out.token(Tone::Keyword, "tcpflags")?;
            let comma = out.list((), tone).comma;
            out.token(tone, TcpFlags { value, care, comma })
        }
        Some(TcpMatch::Established) => {
            let tone = if tone == Tone::Muted { tone } else { Tone::Keyword };

            out.token(tone, "established")
        }
        None => Ok(()),
    }
}

/// Flags that must be set, or clear with `!`.
struct TcpFlags {
    value: u8,
    care: u8,
    comma: Painted<&'static str>,
}

impl Display for TcpFlags {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let flags = TCP_FLAGS
            .iter()
            .enumerate()
            .filter(|(bit, _)| self.care & (1 << bit) != 0);

        for (idx, (bit, name)) in flags.enumerate() {
            if idx > 0 {
                write!(f, "{}", self.comma)?;
            }
            if self.value & (1 << bit) == 0 {
                f.write_str("!")?;
            }
            f.write_str(name)?;
        }

        Ok(())
    }
}
