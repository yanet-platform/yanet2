use core::net::Ipv6Addr;

use zerocopy::{
    FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned,
    byteorder::network_endian::{U16, U32},
};

use crate::{ParseError, split_header, split_header_mut};

/// IPv6 fixed header. Extension headers are part of the payload.
///
/// The header has no checksum, so setters are plain stores. A rewritten
/// address must be reported to the L4 header to fix its pseudo-header
/// checksum.
#[derive(Debug, Clone, Copy, PartialEq, Eq, FromBytes, IntoBytes, KnownLayout, Immutable, Unaligned)]
#[repr(C)]
pub struct Ipv6Header {
    version_tc_flow: U32,
    payload_len: U16,
    next_header: u8,
    hop_limit: u8,
    src: [u8; 16],
    dst: [u8; 16],
}

impl Ipv6Header {
    pub const LEN: usize = 40;

    /// Splits the header and its payload off the front of the input.
    ///
    /// A zero payload length (jumbogram) yields an empty payload.
    ///
    /// # Errors
    ///
    /// - [`ParseError::Truncated`] when the input is shorter than the header.
    /// - [`ParseError::Version`] when the version is not 6.
    /// - [`ParseError::LengthBeyondInput`] when the header and the payload
    ///   length together are beyond the input.
    pub fn parse(buf: &[u8]) -> Result<(&Self, &[u8]), ParseError> {
        let available = buf.len();
        let (header, rest) = split_header::<Self>(buf)?;
        let payload_len = header.validated_payload_len(available)?;
        Ok((header, &rest[..payload_len]))
    }

    /// Same as [`Ipv6Header::parse`] for in-place editing.
    ///
    /// # Errors
    ///
    /// The same as [`Ipv6Header::parse`].
    pub fn parse_mut(buf: &mut [u8]) -> Result<(&mut Self, &mut [u8]), ParseError> {
        let available = buf.len();
        let (header, rest) = split_header_mut::<Self>(buf)?;
        let payload_len = header.validated_payload_len(available)?;
        Ok((header, &mut rest[..payload_len]))
    }

    /// Payload length from the header, once the version and the input agree.
    fn validated_payload_len(&self, available: usize) -> Result<usize, ParseError> {
        if self.version() != 6 {
            return Err(ParseError::Version { found: self.version() });
        }
        let payload_len = usize::from(self.payload_len());
        let length = Self::LEN + payload_len;
        if length > available {
            return Err(ParseError::LengthBeyondInput { length, available });
        }
        Ok(payload_len)
    }

    pub fn version(&self) -> u8 {
        (self.version_tc_flow.get() >> 28) as u8
    }

    pub fn traffic_class(&self) -> u8 {
        (self.version_tc_flow.get() >> 20) as u8
    }

    pub fn flow_label(&self) -> u32 {
        self.version_tc_flow.get() & 0x000f_ffff
    }

    pub fn payload_len(&self) -> u16 {
        self.payload_len.get()
    }

    pub fn next_header(&self) -> u8 {
        self.next_header
    }

    pub fn hop_limit(&self) -> u8 {
        self.hop_limit
    }

    pub fn src(&self) -> Ipv6Addr {
        Ipv6Addr::from(self.src)
    }

    pub fn dst(&self) -> Ipv6Addr {
        Ipv6Addr::from(self.dst)
    }

    pub fn set_traffic_class(&mut self, traffic_class: u8) {
        let word = self.version_tc_flow.get() & !0x0ff0_0000;
        self.version_tc_flow.set(word | (u32::from(traffic_class) << 20));
    }

    pub fn set_hop_limit(&mut self, hop_limit: u8) {
        self.hop_limit = hop_limit;
    }

    /// Decrements the hop limit and returns the new value.
    ///
    /// Returns `None` and leaves the header untouched when the limit is
    /// already 1 or 0, i.e. the packet must not be forwarded.
    #[must_use = "a packet whose hop limit cannot be decremented must not be forwarded"]
    pub fn decrement_hop_limit(&mut self) -> Option<u8> {
        if self.hop_limit <= 1 {
            return None;
        }
        self.hop_limit -= 1;
        Some(self.hop_limit)
    }

    /// Replaces the source address and returns the previous one.
    pub fn set_src(&mut self, addr: Ipv6Addr) -> Ipv6Addr {
        Ipv6Addr::from(core::mem::replace(&mut self.src, addr.octets()))
    }

    /// Replaces the destination address and returns the previous one.
    pub fn set_dst(&mut self, addr: Ipv6Addr) -> Ipv6Addr {
        Ipv6Addr::from(core::mem::replace(&mut self.dst, addr.octets()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// An IPv6 packet with a fixed header and a 6-byte payload.
    fn packet() -> [u8; 46] {
        let mut p = [0u8; 46];
        p[0] = 0x6b;
        p[1] = 0x8a;
        p[2] = 0xbc;
        p[3] = 0xde;
        p[5] = 6;
        p[6] = 17;
        p[7] = 64;
        p[8..24].copy_from_slice(&Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1).octets());
        p[24..40].copy_from_slice(&Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 2).octets());
        p[40..].copy_from_slice(&[1, 2, 3, 4, 5, 6]);
        p
    }

    #[test]
    fn test_ipv6_parse_fields() {
        let buf = packet();
        let (h, payload) = Ipv6Header::parse(&buf).unwrap();
        assert_eq!(6, h.version());
        assert_eq!(0xb8, h.traffic_class());
        assert_eq!(0xabcde, h.flow_label());
        assert_eq!(6, h.payload_len());
        assert_eq!(17, h.next_header());
        assert_eq!(64, h.hop_limit());
        assert_eq!(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1), h.src());
        assert_eq!(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 2), h.dst());
        assert_eq!([1, 2, 3, 4, 5, 6], payload);
    }

    #[test]
    fn test_ipv6_refuses_short_buffer() {
        let buf = packet();
        for len in 0..Ipv6Header::LEN {
            let expected = ParseError::Truncated { needed: 40, available: len };
            assert_eq!(expected, Ipv6Header::parse(&buf[..len]).unwrap_err(), "len {len}");
        }
        let mut copy = buf;
        let expected = ParseError::Truncated { needed: 40, available: 39 };
        assert_eq!(expected, Ipv6Header::parse_mut(&mut copy[..39]).unwrap_err());
    }

    #[test]
    fn test_ipv6_refuses_payload_len_beyond_input() {
        let buf = packet();
        for len in Ipv6Header::LEN..buf.len() {
            let expected = ParseError::LengthBeyondInput { length: 46, available: len };
            assert_eq!(expected, Ipv6Header::parse(&buf[..len]).unwrap_err(), "len {len}");
        }
        let mut copy = buf;
        let expected = ParseError::LengthBeyondInput { length: 46, available: 45 };
        assert_eq!(expected, Ipv6Header::parse_mut(&mut copy[..45]).unwrap_err());
    }

    #[test]
    fn test_ipv6_zero_payload_len_yields_empty_payload() {
        let mut buf = packet();
        buf[4] = 0;
        buf[5] = 0;
        let (h, payload) = Ipv6Header::parse(&buf).unwrap();
        assert_eq!(0, h.payload_len());
        assert!(payload.is_empty());
    }

    #[test]
    fn test_ipv6_trailing_bytes_are_not_payload() {
        let mut buf = [0u8; 50];
        buf[..46].copy_from_slice(&packet());
        let (_, payload) = Ipv6Header::parse(&buf).unwrap();
        assert_eq!(6, payload.len());
    }

    #[test]
    fn test_ipv6_refuses_wrong_version() {
        let mut wrong = packet();
        wrong[0] = 0x45;
        let expected = ParseError::Version { found: 4 };
        assert_eq!(expected, Ipv6Header::parse(&wrong).unwrap_err());
        assert_eq!(expected, Ipv6Header::parse_mut(&mut wrong).unwrap_err());
        // The version is checked before the length.
        wrong[0] = 0x75;
        wrong[5] = 0xff;
        let expected = ParseError::Version { found: 7 };
        assert_eq!(expected, Ipv6Header::parse(&wrong).unwrap_err());
    }

    #[test]
    fn test_ipv6_hop_limit_decrement_stops_at_one() {
        let mut buf = packet();
        let (h, _) = Ipv6Header::parse_mut(&mut buf).unwrap();
        assert_eq!(Some(63), h.decrement_hop_limit());
        h.set_hop_limit(1);
        assert_eq!(None, h.decrement_hop_limit());
        assert_eq!(1, h.hop_limit());
        h.set_hop_limit(0);
        assert_eq!(None, h.decrement_hop_limit());
    }

    #[test]
    fn test_ipv6_set_traffic_class_keeps_flow_label() {
        let mut buf = packet();
        let (h, _) = Ipv6Header::parse_mut(&mut buf).unwrap();
        h.set_traffic_class(0x2e);
        assert_eq!(0x2e, h.traffic_class());
        assert_eq!(0xabcde, h.flow_label());
        assert_eq!(6, h.version());
    }

    #[test]
    fn test_ipv6_set_addresses_returns_previous() {
        let mut buf = packet();
        let (h, _) = Ipv6Header::parse_mut(&mut buf).unwrap();
        let new = Ipv6Addr::new(0xfd00, 0, 0, 0, 0, 0, 0, 9);
        assert_eq!(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1), h.set_src(new));
        assert_eq!(new, h.src());
        assert_eq!(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 2), h.set_dst(new));
    }
}
