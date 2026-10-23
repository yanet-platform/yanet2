use core::net::Ipv6Addr;

use zerocopy::{FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned, byteorder::network_endian::U16};

use crate::checksum;

/// ICMPv4 header: type, code, checksum and four bytes of message-specific
/// data. The checksum has no pseudo header.
#[derive(Debug, Clone, Copy, PartialEq, Eq, FromBytes, IntoBytes, KnownLayout, Immutable, Unaligned)]
#[repr(C)]
pub struct Icmpv4Header {
    icmp_type: u8,
    code: u8,
    checksum: U16,
    rest: [u8; 4],
}

impl Icmpv4Header {
    pub const LEN: usize = 8;

    /// Splits the header off the front of `buf`.
    pub fn parse(buf: &[u8]) -> Option<(&Self, &[u8])> {
        Self::ref_from_prefix(buf).ok()
    }

    /// Splits the header off the front of `buf` for in-place editing.
    pub fn parse_mut(buf: &mut [u8]) -> Option<(&mut Self, &mut [u8])> {
        Self::mut_from_prefix(buf).ok()
    }

    pub fn icmp_type(&self) -> u8 {
        self.icmp_type
    }

    pub fn code(&self) -> u8 {
        self.code
    }

    pub fn checksum(&self) -> u16 {
        self.checksum.get()
    }

    /// Message-specific bytes (identifier and sequence, MTU, pointer...).
    pub fn rest(&self) -> [u8; 4] {
        self.rest
    }

    pub fn set_type_code(&mut self, icmp_type: u8, code: u8) {
        let old = u16::from_be_bytes([self.icmp_type, self.code]);
        self.icmp_type = icmp_type;
        self.code = code;
        let new = u16::from_be_bytes([icmp_type, code]);
        self.checksum.set(checksum::update(self.checksum.get(), old, new));
    }

    pub fn set_rest(&mut self, rest: [u8; 4]) {
        let old = core::mem::replace(&mut self.rest, rest);
        self.checksum
            .set(checksum::update_bytes(self.checksum.get(), &old, &rest));
    }
}

/// ICMPv6 header; the checksum covers the IPv6 pseudo header.
#[derive(Debug, Clone, Copy, PartialEq, Eq, FromBytes, IntoBytes, KnownLayout, Immutable, Unaligned)]
#[repr(C)]
pub struct Icmpv6Header {
    icmp_type: u8,
    code: u8,
    checksum: U16,
    rest: [u8; 4],
}

impl Icmpv6Header {
    pub const LEN: usize = 8;

    /// Splits the header off the front of `buf`.
    pub fn parse(buf: &[u8]) -> Option<(&Self, &[u8])> {
        Self::ref_from_prefix(buf).ok()
    }

    /// Splits the header off the front of `buf` for in-place editing.
    pub fn parse_mut(buf: &mut [u8]) -> Option<(&mut Self, &mut [u8])> {
        Self::mut_from_prefix(buf).ok()
    }

    pub fn icmp_type(&self) -> u8 {
        self.icmp_type
    }

    pub fn code(&self) -> u8 {
        self.code
    }

    pub fn checksum(&self) -> u16 {
        self.checksum.get()
    }

    /// Message-specific bytes (identifier and sequence, MTU, pointer...).
    pub fn rest(&self) -> [u8; 4] {
        self.rest
    }

    pub fn set_type_code(&mut self, icmp_type: u8, code: u8) {
        let old = u16::from_be_bytes([self.icmp_type, self.code]);
        self.icmp_type = icmp_type;
        self.code = code;
        let new = u16::from_be_bytes([icmp_type, code]);
        self.checksum.set(checksum::update(self.checksum.get(), old, new));
    }

    pub fn set_rest(&mut self, rest: [u8; 4]) {
        let old = core::mem::replace(&mut self.rest, rest);
        self.checksum
            .set(checksum::update_bytes(self.checksum.get(), &old, &rest));
    }

    /// Fixes the checksum after the IPv6 source or destination changed.
    pub fn replace_ipv6_addr(&mut self, old: Ipv6Addr, new: Ipv6Addr) {
        self.checksum.set(checksum::update_bytes(
            self.checksum.get(),
            &old.octets(),
            &new.octets(),
        ));
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::IPPROTO_ICMPV6;

    /// An ICMPv4 echo request carrying `payload`, and its length.
    fn echo(payload: &[u8]) -> ([u8; 32], usize) {
        let mut buf = [0u8; 32];
        let total = 8 + payload.len();
        buf[0] = 8;
        buf[4..6].copy_from_slice(&0x1234u16.to_be_bytes());
        buf[6..8].copy_from_slice(&7u16.to_be_bytes());
        buf[8..total].copy_from_slice(payload);
        let sum = !checksum::sum(&buf[..total]);
        buf[2..4].copy_from_slice(&sum.to_be_bytes());
        (buf, total)
    }

    #[test]
    fn test_icmpv4_parse_fields_and_refuse_short() {
        let (buf, total) = echo(&[1, 2, 3]);
        let (h, payload) = Icmpv4Header::parse(&buf[..total]).unwrap();
        assert_eq!(8, h.icmp_type());
        assert_eq!(0, h.code());
        assert_eq!([0x12, 0x34, 0, 7], h.rest());
        assert_eq!([1, 2, 3], payload);
        for len in 0..Icmpv4Header::LEN {
            assert!(Icmpv4Header::parse(&buf[..len]).is_none(), "len {len}");
        }
    }

    #[test]
    fn test_icmpv4_incremental_checksum_after_mutations() {
        let (mut buf, total) = echo(&[1, 2, 3, 4, 5]);
        for i in 0..=255u8 {
            let (h, _) = Icmpv4Header::parse_mut(&mut buf[..total]).unwrap();
            h.set_type_code(i, 255 - i);
            h.set_rest([i, i ^ 0x77, 255 - i, i.wrapping_mul(3)]);
            assert_eq!(0xffff, checksum::sum(&buf[..total]), "step {i}");
        }
    }

    #[test]
    fn test_icmpv6_parse_refuses_short_buffer() {
        let buf = [0u8; 8];
        for len in 0..Icmpv6Header::LEN {
            assert!(Icmpv6Header::parse(&buf[..len]).is_none(), "len {len}");
        }
        assert!(Icmpv6Header::parse(&buf).is_some());
    }

    #[test]
    fn test_icmpv6_incremental_checksum_for_mutations_and_address() {
        let mut src = Ipv6Addr::new(0xfe80, 0, 0, 0, 0, 0, 0, 1);
        let dst = Ipv6Addr::new(0xff02, 0, 0, 0, 0, 0, 0, 2);
        let (mut buf, total) = echo(&[1, 2, 3, 4]);
        buf[0] = 128;
        buf[2] = 0;
        buf[3] = 0;
        let sum = checksum::l4_checksum_ipv6(src, dst, IPPROTO_ICMPV6, &buf[..total]);
        buf[2..4].copy_from_slice(&sum.to_be_bytes());
        for i in 0..=255u16 {
            let new_src = Ipv6Addr::new(0x2001, i, 0xffff - i, 0, i, 3, 0xfffe, i);
            let (h, _) = Icmpv6Header::parse_mut(&mut buf[..total]).unwrap();
            h.set_type_code(129, (i % 7) as u8);
            h.replace_ipv6_addr(src, new_src);
            src = new_src;
            assert!(
                checksum::l4_is_valid_ipv6(src, dst, IPPROTO_ICMPV6, &buf[..total]),
                "step {i}"
            );
        }
    }
}
