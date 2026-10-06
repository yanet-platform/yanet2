//! Shared protocol number constants.
//!
//! The values match the kernel/DPDK assignments the dataplane parser and
//! the filter signatures already use, so a module can dispatch on them
//! without repeating magic numbers.

/// Ether type numbers as stored on the wire.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u16)]
pub enum EtherType {
    Ipv4 = 0x0800,
    Arp = 0x0806,
    Vlan = 0x8100,
    Ipv6 = 0x86DD,
}

impl EtherType {
    /// The wire value, in host byte order of the raw u16.
    pub const fn wire(self) -> u16 {
        self as u16
    }

    /// Parse an on-wire (big-endian-loaded) ether type value.
    pub const fn from_wire(wire: u16) -> Option<Self> {
        match wire {
            0x0800 => Some(Self::Ipv4),
            0x0806 => Some(Self::Arp),
            0x8100 => Some(Self::Vlan),
            0x86DD => Some(Self::Ipv6),
            _ => None,
        }
    }
}

/// IP protocol numbers, shared by the IPv4 protocol field, the IPv6 next
/// header field and the packet transport type.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum IpProtocol {
    HopByHop = 0,
    Icmp = 1,
    Igmp = 2,
    IpIp = 4,
    Tcp = 6,
    Udp = 17,
    Ipv6 = 41,
    Routing = 43,
    Fragment = 44,
    Gre = 47,
    Esp = 50,
    Ah = 51,
    Icmpv6 = 58,
    NoNext = 59,
    Destination = 60,
}

impl IpProtocol {
    /// Parse a raw protocol number.
    pub const fn from_raw(raw: u8) -> Option<Self> {
        match raw {
            0 => Some(Self::HopByHop),
            1 => Some(Self::Icmp),
            2 => Some(Self::Igmp),
            4 => Some(Self::IpIp),
            6 => Some(Self::Tcp),
            17 => Some(Self::Udp),
            41 => Some(Self::Ipv6),
            43 => Some(Self::Routing),
            44 => Some(Self::Fragment),
            47 => Some(Self::Gre),
            50 => Some(Self::Esp),
            51 => Some(Self::Ah),
            58 => Some(Self::Icmpv6),
            59 => Some(Self::NoNext),
            60 => Some(Self::Destination),
            _ => None,
        }
    }
}
