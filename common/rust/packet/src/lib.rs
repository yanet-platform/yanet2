//! Network packet header views for YANET dataplane modules.
//!
//! Every header is a borrow over a byte slice in network byte order: the
//! views hold no data of their own, so they compose freely with the
//! zero-copy packet buffers the dataplane hands to modules. Field accessors
//! read and write big-endian values in place; nothing here can panic on a
//! short buffer because [`from_bytes`] refuses one.
//!
//! [`from_bytes`]: Ipv4::from_bytes
#![no_std]

mod checksum;
mod ether;
mod ip4;
mod ip6;
mod protocol;
mod transport;
mod tunnel;

pub use checksum::{Checksum, checksum, checksum_compose, checksum_update, verify};
pub use ether::{Arp, Eth2, Eth2Mut, Vlan};
pub use ip4::{Ipv4, Ipv4Mut};
pub use ip6::{Ipv6, Ipv6Ext, Ipv6ExtFragment, Ipv6ExtHopByHop, Ipv6ExtIter, Ipv6ExtRouting, Ipv6Mut};
pub use protocol::{EtherType, IpProtocol};
pub use transport::{Icmp, IcmpMut, Tcp, TcpMut, Udp, UdpMut};
pub use tunnel::{Gre, GreMut};

#[cfg(test)]
extern crate std;

#[cfg(test)]
mod tests;
