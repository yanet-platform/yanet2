//! Byte-level packet headers for the YANET2 module SDK.
//!
//! The crate works on byte slices only. A header view is built at the offset
//! where the header starts and refuses short or malformed input by returning
//! `None`; nothing here panics on attacker-controlled bytes.

#![no_std]
#![forbid(unsafe_code)]

pub mod checksum;
mod ethernet;
mod icmp;
mod ipv4;
mod ipv6;
mod tcp;
mod udp;

pub use ethernet::{ETHERTYPE_IPV4, ETHERTYPE_IPV6, ETHERTYPE_QINQ, ETHERTYPE_VLAN, EthernetHeader, VlanTag};
pub use icmp::{Icmpv4Header, Icmpv6Header};
pub use ipv4::{Ipv4Header, Ipv4Packet, Ipv4PacketMut};
pub use ipv6::Ipv6Header;
pub use tcp::{TcpHeader, TcpPacket, TcpPacketMut};
pub use udp::UdpHeader;

/// IP protocol number of ICMP.
pub const IPPROTO_ICMP: u8 = 1;
/// IP protocol number of TCP.
pub const IPPROTO_TCP: u8 = 6;
/// IP protocol number of UDP.
pub const IPPROTO_UDP: u8 = 17;
/// IP protocol number of ICMPv6.
pub const IPPROTO_ICMPV6: u8 = 58;
