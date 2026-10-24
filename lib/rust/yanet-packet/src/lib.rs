//! Byte-level packet headers for the YANET2 module SDK.
//!
//! The crate works on byte slices only. A header view is built at the offset
//! where the header starts and refuses short or malformed input with a
//! [`ParseError`] that says why; nothing here panics on attacker-controlled
//! bytes.

#![cfg_attr(not(test), no_std)]
#![forbid(unsafe_code)]

pub mod checksum;
mod error;
mod ethernet;
mod icmp;
mod ipv4;
mod ipv6;
mod tcp;
#[cfg(test)]
mod testing;
mod udp;

pub use error::ParseError;
pub use ethernet::{ETHERTYPE_IPV4, ETHERTYPE_IPV6, ETHERTYPE_QINQ, ETHERTYPE_VLAN, EthernetHeader, VlanTag};
pub use icmp::{Icmpv4Header, Icmpv6Header};
pub use ipv4::{Ipv4Header, Ipv4Packet, Ipv4PacketMut};
pub use ipv6::Ipv6Header;
pub use tcp::{TcpHeader, TcpPacket, TcpPacketMut};
pub use udp::UdpHeader;
use zerocopy::{FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned};

/// Splits a header of type `T` off the front of the input.
///
/// Fails with [`ParseError::Truncated`] when the input is shorter than `T`.
fn split_header<T>(buf: &[u8]) -> Result<(&T, &[u8]), ParseError>
where
    T: FromBytes + KnownLayout + Immutable + Unaligned,
{
    T::ref_from_prefix(buf).map_err(|_| ParseError::Truncated {
        needed: size_of::<T>(),
        available: buf.len(),
    })
}

/// Splits a header of type `T` off the front of the input for in-place
/// editing.
///
/// Fails like [`split_header`].
fn split_header_mut<T>(buf: &mut [u8]) -> Result<(&mut T, &mut [u8]), ParseError>
where
    T: FromBytes + IntoBytes + KnownLayout + Unaligned,
{
    let available = buf.len();
    T::mut_from_prefix(buf).map_err(|_| ParseError::Truncated { needed: size_of::<T>(), available })
}

/// IP protocol number of ICMP.
pub const IPPROTO_ICMP: u8 = 1;
/// IP protocol number of TCP.
pub const IPPROTO_TCP: u8 = 6;
/// IP protocol number of UDP.
pub const IPPROTO_UDP: u8 = 17;
/// IP protocol number of ICMPv6.
pub const IPPROTO_ICMPV6: u8 = 58;
