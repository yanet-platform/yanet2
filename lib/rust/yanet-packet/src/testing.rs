//! Shared strategies and checks of the property tests.

use core::net::{Ipv4Addr, Ipv6Addr};

use proptest::{collection::vec, option, prelude::*, sample::Index};

use crate::{ParseError, checksum};

/// Run configuration without a failure file in the source tree.
///
/// Under Miri a property runs few cases: interpretation is slow, the crate
/// forbids unsafe code, and the example tests already drive every cast.
pub fn config() -> ProptestConfig {
    ProptestConfig {
        cases: if cfg!(miri) { 4 } else { 512 },
        failure_persistence: None,
        ..ProptestConfig::default()
    }
}

/// Pure random bytes, or a valid packet with lying fields and a cut tail.
///
/// Random bytes alone die at the first check; the mangled ones reach the
/// length checks behind it. The offsets are those of the header fields the
/// checks read, the only bytes the lies touch.
pub fn input(valid: impl Strategy<Value = Vec<u8>>, lies: &'static [usize]) -> impl Strategy<Value = Vec<u8>> {
    prop_oneof![
        1 => vec(any::<u8>(), 0..=96),
        3 => mangled(valid, lies),
    ]
}

/// A packet with up to three lying bytes and a random cut.
fn mangled(valid: impl Strategy<Value = Vec<u8>>, lies: &'static [usize]) -> impl Strategy<Value = Vec<u8>> {
    let byte = prop_oneof![2 => any::<u8>(), 1 => 0u8..=16, 1 => 0x40u8..=0x6f];
    let patches = vec((any::<Index>(), byte), 0..=3);
    (valid, patches, option::of(any::<Index>())).prop_map(move |(mut bytes, patches, cut)| {
        for (at, byte) in patches {
            if let Some(target) = bytes.get_mut(lies[at.index(lies.len())]) {
                *target = byte;
            }
        }
        if let Some(cut) = cut {
            bytes.truncate(cut.index(bytes.len() + 1));
        }
        bytes
    })
}

/// Asserts that the parts are adjacent sub-slices of the input, from offset 0.
pub fn assert_adjacent(input: &[u8], parts: &[&[u8]]) {
    let start = input.as_ptr().addr();
    let mut next = start;
    for part in parts {
        assert_eq!(next, part.as_ptr().addr());
        next += part.len();
    }
    assert!(next <= start + input.len());
}

/// The big-endian 16-bit field at the start of the bytes.
pub fn be16(bytes: &[u8]) -> usize {
    usize::from(u16::from_be_bytes([bytes[0], bytes[1]]))
}

/// The part lengths of a parse, or its error.
pub fn lens(parts: &Result<Vec<&[u8]>, ParseError>) -> Result<Vec<usize>, ParseError> {
    parts
        .as_ref()
        .map(|parts| parts.iter().map(|part| part.len()).collect())
        .map_err(|&e| e)
}

/// Zero and all-ones are the two signs of a one's-complement zero.
pub fn same_checksum(a: u16, b: u16) -> bool {
    a == b || (matches!(a, 0 | 0xffff) && matches!(b, 0 | 0xffff))
}

/// The IP version whose pseudo header covers an L4 checksum.
#[derive(Debug, Clone, Copy)]
pub enum Family {
    V4,
    V6,
}

/// Either IP family; IPv4 addresses use the first four bytes.
pub fn family() -> impl Strategy<Value = Family> {
    prop_oneof![Just(Family::V4), Just(Family::V6)]
}

impl Family {
    /// The IPv4 address held in the first four bytes.
    pub fn v4(addr: [u8; 16]) -> Ipv4Addr {
        Ipv4Addr::new(addr[0], addr[1], addr[2], addr[3])
    }

    /// Full L4 checksum of a segment whose checksum field is zero.
    pub fn l4_checksum(self, src: [u8; 16], dst: [u8; 16], protocol: u8, segment: &[u8]) -> u16 {
        match self {
            Self::V4 => checksum::l4_checksum_ipv4(Self::v4(src), Self::v4(dst), protocol, segment),
            Self::V6 => checksum::l4_checksum_ipv6(Ipv6Addr::from(src), Ipv6Addr::from(dst), protocol, segment),
        }
    }

    /// Whether the segment's checksum verifies over the pseudo header.
    pub fn l4_is_valid(self, src: [u8; 16], dst: [u8; 16], protocol: u8, segment: &[u8]) -> bool {
        match self {
            Self::V4 => checksum::l4_is_valid_ipv4(Self::v4(src), Self::v4(dst), protocol, segment),
            Self::V6 => checksum::l4_is_valid_ipv6(Ipv6Addr::from(src), Ipv6Addr::from(dst), protocol, segment),
        }
    }
}

/// A mutation of an L4 header: a port or a pseudo-header address changes.
#[derive(Debug, Clone)]
pub enum L4Op {
    SrcPort(u16),
    DstPort(u16),
    Src([u8; 16]),
    Dst([u8; 16]),
}

/// Random sequences of L4 mutations.
pub fn l4_ops() -> impl Strategy<Value = Vec<L4Op>> {
    let op = prop_oneof![
        any::<u16>().prop_map(L4Op::SrcPort),
        any::<u16>().prop_map(L4Op::DstPort),
        any::<[u8; 16]>().prop_map(L4Op::Src),
        any::<[u8; 16]>().prop_map(L4Op::Dst),
    ];
    vec(op, 0..=16)
}

/// The setters TCP and UDP headers share, for the common mutation driver.
pub trait L4Header {
    fn set_src_port(&mut self, port: u16);
    fn set_dst_port(&mut self, port: u16);
    fn replace_ipv4_addr(&mut self, old: Ipv4Addr, new: Ipv4Addr);
    fn replace_ipv6_addr(&mut self, old: Ipv6Addr, new: Ipv6Addr);
}

/// Applies the mutation and tracks the pseudo-header source and destination.
pub fn apply<H: L4Header>(header: &mut H, family: Family, op: &L4Op, addrs: &mut ([u8; 16], [u8; 16])) {
    let (addr, new) = match *op {
        L4Op::SrcPort(port) => return header.set_src_port(port),
        L4Op::DstPort(port) => return header.set_dst_port(port),
        L4Op::Src(new) => (&mut addrs.0, new),
        L4Op::Dst(new) => (&mut addrs.1, new),
    };
    match family {
        Family::V4 => header.replace_ipv4_addr(Family::v4(*addr), Family::v4(new)),
        Family::V6 => header.replace_ipv6_addr(Ipv6Addr::from(*addr), Ipv6Addr::from(new)),
    }
    *addr = new;
}
