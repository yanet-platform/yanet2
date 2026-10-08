//! VXLAN tunnel device over an IPv4 underlay, in safe Rust.
//!
//! Output encapsulates every frame in outer Ethernet, IPv4, UDP and VXLAN
//! headers built from the device config. Input decapsulates VXLAN packets
//! addressed to the local endpoint with the configured VNI, drops VXLAN
//! packets addressed to the local endpoint that carry another VNI or are
//! malformed, and passes all other traffic unchanged: the underlay port
//! still carries the router's own non-tunnel traffic.

#![cfg_attr(not(test), no_std)]
#![forbid(unsafe_code)]

use core::mem::size_of;

use yanet_dp_sys::{Device, Packet, Verdict};
pub use yanet_vxlan_config::{TYPE_NAME, VNI_MAX, VXLAN_PORT, VxlanConfig};
use zerocopy::{
    FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned,
    byteorder::network_endian::{U16, U32},
};

const ETHER_TYPE_IPV4: u16 = 0x0800;
const IP_PROTO_UDP: u8 = 17;
const IPV4_FLAG_DF: u16 = 0x4000;
const IPV4_FLAG_MF: u16 = 0x2000;
const IPV4_FRAGMENT_OFFSET_MASK: u16 = 0x1fff;
const OUTER_TTL: u8 = 64;
const VXLAN_FLAG_VNI: u8 = 0x08;
const VNI_MASK: u32 = VNI_MAX;

#[derive(Clone, Copy, Debug, FromBytes, IntoBytes, Immutable, KnownLayout, Unaligned)]
#[repr(C)]
struct EtherHeader {
    dst: [u8; 6],
    src: [u8; 6],
    ether_type: U16,
}

#[derive(Clone, Copy, Debug, FromBytes, IntoBytes, Immutable, KnownLayout, Unaligned)]
#[repr(C)]
struct Ipv4Header {
    version_ihl: u8,
    tos: u8,
    total_len: U16,
    id: U16,
    flags_fragment: U16,
    ttl: u8,
    protocol: u8,
    checksum: U16,
    src: [u8; 4],
    dst: [u8; 4],
}

#[derive(Clone, Copy, Debug, FromBytes, IntoBytes, Immutable, KnownLayout, Unaligned)]
#[repr(C)]
struct UdpHeader {
    src_port: U16,
    dst_port: U16,
    len: U16,
    checksum: U16,
}

#[derive(Clone, Copy, Debug, FromBytes, IntoBytes, Immutable, KnownLayout, Unaligned)]
#[repr(C)]
struct VxlanHeader {
    flags: u8,
    reserved: [u8; 3],
    // The VNI in the upper 24 bits, a reserved byte below.
    vni_reserved: U32,
}

/// Every header the encapsulation prepends, in wire order.
#[derive(Clone, Copy, Debug, IntoBytes, Immutable)]
#[repr(C)]
pub struct OuterHeaders {
    ether: EtherHeader,
    ipv4: Ipv4Header,
    udp: UdpHeader,
    vxlan: VxlanHeader,
}

/// Bytes the encapsulation adds in front of the inner frame.
pub const ENCAP_LEN: usize = size_of::<OuterHeaders>();

const _: () = assert!(ENCAP_LEN == 50);

const ETHER_LEN: usize = size_of::<EtherHeader>();
const IPV4_MIN_LEN: usize = size_of::<Ipv4Header>();
const UDP_VXLAN_LEN: usize = size_of::<UdpHeader>() + size_of::<VxlanHeader>();

/// Returns the one's complement checksum of an IPv4 header.
fn ipv4_checksum(header: &[u8]) -> u16 {
    let mut sum = header
        .chunks(2)
        .map(|word| u32::from(word[0]) << 8 | word.get(1).copied().map_or(0, u32::from))
        .sum::<u32>();
    while sum > 0xffff {
        sum = (sum & 0xffff) + (sum >> 16);
    }
    !(sum as u16)
}

/// Maps a flow hash to an outer UDP source port in 49152..=65535.
///
/// RFC 7348 section 5 recommends a hash of inner-packet fields for ECMP
/// entropy, in the dynamic range of RFC 6335. The packet parser hashes the
/// inner addresses and L4 ports, so one inner flow keeps one port and
/// distinct flows spread. A frame the parser hashes nothing for, such as
/// ARP, has hash zero and always maps to port 49152.
pub fn source_port(hash: u32) -> u16 {
    let folded = (hash ^ (hash >> 16)) as u16;
    0xc000 | (folded & 0x3fff)
}

/// Builds the outer headers for an inner frame of the given length.
///
/// Follows RFC 7348 section 5: UDP destination port 4789, UDP checksum
/// zero over IPv4, the I flag set and every reserved bit zero. Returns
/// `None` when the encapsulated packet would exceed the largest IPv4
/// datagram.
pub fn outer_headers(config: &VxlanConfig, hash: u32, inner_len: usize) -> Option<OuterHeaders> {
    let udp_len = u16::try_from(UDP_VXLAN_LEN + inner_len).ok()?;
    let total_len = u16::try_from(IPV4_MIN_LEN + usize::from(udp_len)).ok()?;

    let mut headers = OuterHeaders {
        ether: EtherHeader {
            dst: config.remote_mac,
            src: config.local_mac,
            ether_type: U16::new(ETHER_TYPE_IPV4),
        },
        ipv4: Ipv4Header {
            version_ihl: 0x45,
            tos: 0,
            total_len: U16::new(total_len),
            id: U16::new(0),
            flags_fragment: U16::new(IPV4_FLAG_DF),
            ttl: OUTER_TTL,
            protocol: IP_PROTO_UDP,
            checksum: U16::new(0),
            src: config.local_ip,
            dst: config.remote_ip,
        },
        udp: UdpHeader {
            src_port: U16::new(source_port(hash)),
            dst_port: U16::new(VXLAN_PORT),
            len: U16::new(udp_len),
            checksum: U16::new(0),
        },
        vxlan: VxlanHeader {
            flags: VXLAN_FLAG_VNI,
            reserved: [0; 3],
            vni_reserved: U32::new((config.vni & VNI_MASK) << 8),
        },
    };
    headers.ipv4.checksum = U16::new(ipv4_checksum(headers.ipv4.as_bytes()));
    Some(headers)
}

/// What the input side does with a received frame.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Ingress {
    /// Strip this many leading bytes and continue with the inner frame.
    Decap(usize),
    /// Not tunnel traffic for this device: continue unchanged.
    Pass,
    /// Tunnel traffic for the local endpoint that this device must not
    /// accept.
    Drop,
}

/// Classifies a received frame, given the bytes of its first segment.
///
/// Only an unfragmented IPv4 UDP datagram to the VXLAN port of the local
/// address is tunnel traffic; anything else passes. Tunnel traffic with the
/// I flag and the configured VNI decapsulates when the whole outer stack and
/// an inner Ethernet header fit in the first segment; otherwise it drops.
/// A zero UDP checksum is accepted and the reserved bits are ignored, as RFC
/// 7348 section 5 requires; a non-zero checksum is not verified.
pub fn classify_ingress(config: &VxlanConfig, frame: &[u8]) -> Ingress {
    let Ok((ether, rest)) = EtherHeader::ref_from_prefix(frame) else {
        return Ingress::Pass;
    };
    if ether.ether_type.get() != ETHER_TYPE_IPV4 {
        return Ingress::Pass;
    }
    let Ok((ipv4, _)) = Ipv4Header::ref_from_prefix(rest) else {
        return Ingress::Pass;
    };
    let ihl = usize::from(ipv4.version_ihl & 0x0f) * 4;
    let fragment = ipv4.flags_fragment.get();
    if ipv4.version_ihl >> 4 != 4
        || ihl < IPV4_MIN_LEN
        || ipv4.protocol != IP_PROTO_UDP
        || ipv4.dst != config.local_ip
        || fragment & (IPV4_FLAG_MF | IPV4_FRAGMENT_OFFSET_MASK) != 0
    {
        return Ingress::Pass;
    }
    let Some(udp_bytes) = rest.get(ihl..) else {
        return Ingress::Drop;
    };
    let Ok((udp, rest)) = UdpHeader::ref_from_prefix(udp_bytes) else {
        return Ingress::Drop;
    };
    if udp.dst_port.get() != VXLAN_PORT {
        return Ingress::Pass;
    }
    let Ok((vxlan, inner)) = VxlanHeader::ref_from_prefix(rest) else {
        return Ingress::Drop;
    };
    if vxlan.flags & VXLAN_FLAG_VNI == 0
        || vxlan.vni_reserved.get() >> 8 != config.vni & VNI_MASK
        || inner.len() < ETHER_LEN
    {
        return Ingress::Drop;
    }
    Ingress::Decap(ETHER_LEN + ihl + UDP_VXLAN_LEN)
}

/// The VXLAN device type.
pub struct Vxlan;

impl Device for Vxlan {
    const NAME: &'static str = TYPE_NAME;
    type Config = VxlanConfig;

    fn input(config: &VxlanConfig, packet: &mut Packet<'_>) -> Verdict {
        match classify_ingress(config, packet.data()) {
            Ingress::Pass => Verdict::Output,
            Ingress::Drop => Verdict::Drop,
            Ingress::Decap(len) => {
                // The strip length is at most 14 + 60 + 16 bytes.
                let Ok(len) = u16::try_from(len) else {
                    return Verdict::Drop;
                };
                if packet.trim_front(len) && packet.reparse() {
                    Verdict::Output
                } else {
                    Verdict::Drop
                }
            }
        }
    }

    fn output(config: &VxlanConfig, packet: &mut Packet<'_>) -> Verdict {
        let inner_len = packet.total_len();
        if packet.data().len() < ETHER_LEN {
            return Verdict::Drop;
        }
        let Some(headers) = outer_headers(config, packet.hash(), inner_len) else {
            return Verdict::Drop;
        };
        let Some(frame) = packet.prepend(ENCAP_LEN as u16) else {
            return Verdict::Drop;
        };
        let Some(outer) = frame.get_mut(..ENCAP_LEN) else {
            return Verdict::Drop;
        };
        outer.copy_from_slice(headers.as_bytes());
        // Later output stages read the parsed offsets of the outer stack.
        if packet.reparse() {
            Verdict::Output
        } else {
            Verdict::Drop
        }
    }
}

yanet_dp_sys::export_device!(vxlan, Vxlan);

#[cfg(test)]
mod tests;
