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

    use proptest::{collection::vec, prelude::*};

    use super::*;
    use crate::testing;

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

    /// IPv6 header fields as built and read back; the version is always 6.
    #[derive(Debug, Clone, PartialEq, Eq)]
    struct Fields {
        traffic_class: u8,
        flow_label: u32,
        next_header: u8,
        hop_limit: u8,
        src: [u8; 16],
        dst: [u8; 16],
    }

    impl Fields {
        /// The packet bytes, with the payload length of the payload.
        fn to_bytes(&self, payload: &[u8]) -> Vec<u8> {
            let word = 6u32 << 28 | u32::from(self.traffic_class) << 20 | self.flow_label;
            [
                &word.to_be_bytes()[..],
                &(payload.len() as u16).to_be_bytes(),
                &[self.next_header, self.hop_limit],
                &self.src,
                &self.dst,
                payload,
            ]
            .concat()
        }

        fn read(header: &Ipv6Header) -> Self {
            Self {
                traffic_class: header.traffic_class(),
                flow_label: header.flow_label(),
                next_header: header.next_header(),
                hop_limit: header.hop_limit(),
                src: header.src().octets(),
                dst: header.dst().octets(),
            }
        }
    }

    /// Random IPv6 header fields.
    fn fields() -> impl Strategy<Value = Fields> {
        (
            any::<u8>(),
            0u32..1 << 20,
            any::<u8>(),
            any::<u8>(),
            any::<[u8; 16]>(),
            any::<[u8; 16]>(),
        )
            .prop_map(|(traffic_class, flow_label, next_header, hop_limit, src, dst)| Fields {
                traffic_class,
                flow_label,
                next_header,
                hop_limit,
                src,
                dst,
            })
    }

    /// Offsets the property inputs may lie at: the version byte, and both
    /// payload length bytes.
    const LIES: &[usize] = &[0, 4, 5];

    /// A well-formed IPv6 packet.
    fn valid_packet() -> impl Strategy<Value = Vec<u8>> {
        (fields(), vec(any::<u8>(), 0..=32)).prop_map(|(fields, payload)| fields.to_bytes(&payload))
    }

    /// The header and payload parts of a parse, in input order, or its error.
    fn parts(buf: &[u8]) -> Result<Vec<&[u8]>, ParseError> {
        let (header, payload) = Ipv6Header::parse(buf)?;
        Ok(vec![header.as_bytes(), payload])
    }

    /// The parts as above, through the mutable parse.
    fn parts_mut(buf: &mut [u8]) -> Result<Vec<&[u8]>, ParseError> {
        let (header, payload) = Ipv6Header::parse_mut(buf)?;
        Ok(vec![header.as_bytes(), &*payload])
    }

    /// A mutation of the IPv6 header through its setters.
    #[derive(Debug, Clone)]
    enum Op {
        TrafficClass(u8),
        HopLimit(u8),
        Src([u8; 16]),
        Dst([u8; 16]),
    }

    /// A random header mutation.
    fn op() -> impl Strategy<Value = Op> {
        prop_oneof![
            any::<u8>().prop_map(Op::TrafficClass),
            any::<u8>().prop_map(Op::HopLimit),
            any::<[u8; 16]>().prop_map(Op::Src),
            any::<[u8; 16]>().prop_map(Op::Dst),
        ]
    }

    /// Asserts that the error carries the values the input bytes hold.
    fn assert_error(buf: &[u8], error: ParseError) {
        match error {
            ParseError::Truncated { needed, available } => {
                assert_eq!(Ipv6Header::LEN, needed);
                assert_eq!(buf.len(), available);
                assert!(needed > available);
            }
            ParseError::Version { found } => {
                assert_eq!(buf[0] >> 4, found);
                assert_ne!(6, found);
            }
            ParseError::LengthBeyondInput { length, available } => {
                assert_eq!(6, buf[0] >> 4);
                assert_eq!(Ipv6Header::LEN + testing::be16(&buf[4..]), length);
                assert_eq!(buf.len(), available);
                assert!(length > available);
            }
            ParseError::HeaderLen { .. } | ParseError::LengthBelowHeader { .. } => panic!("unexpected {error:?}"),
        }
    }

    proptest! {
        #![proptest_config(testing::config())]

        #[test]
        fn test_ipv6_parse_ok_respects_bounds(buf in testing::input(valid_packet(), LIES)) {
            if let Ok(parts) = parts(&buf) {
                testing::assert_adjacent(&buf, &parts);
                let header = Ipv6Header::read_from_bytes(parts[0]).unwrap();
                prop_assert_eq!(Ipv6Header::LEN, parts[0].len());
                prop_assert_eq!(usize::from(header.payload_len()), parts[1].len());
                prop_assert!(Ipv6Header::LEN + parts[1].len() <= buf.len());
            }
        }

        #[test]
        fn test_ipv6_parse_error_reports_fields_of_input(buf in testing::input(valid_packet(), LIES)) {
            if let Err(error) = parts(&buf) {
                assert_error(&buf, error);
            }
        }

        #[test]
        fn test_ipv6_parse_and_parse_mut_agree(buf in testing::input(valid_packet(), LIES)) {
            let mut copy = buf.clone();
            prop_assert_eq!(testing::lens(&parts(&buf)), testing::lens(&parts_mut(&mut copy)));
        }

        #[test]
        fn test_ipv6_round_trip_reads_back_built_fields(fields in fields(), payload in vec(any::<u8>(), 0..=32)) {
            let buf = fields.to_bytes(&payload);
            let (header, rest) = Ipv6Header::parse(&buf).unwrap();
            prop_assert_eq!(6, header.version());
            prop_assert_eq!(&fields, &Fields::read(header));
            prop_assert_eq!(&payload[..], rest);
        }

        #[test]
        fn test_ipv6_setters_change_only_their_field(
            start in fields(),
            payload in vec(any::<u8>(), 0..=8),
            ops in vec(op(), 0..=12),
        ) {
            let mut buf = start.to_bytes(&payload);
            let mut model = start.clone();
            for op in ops {
                let (header, _) = Ipv6Header::parse_mut(&mut buf).unwrap();
                match op {
                    Op::TrafficClass(traffic_class) => {
                        header.set_traffic_class(traffic_class);
                        model.traffic_class = traffic_class;
                    }
                    Op::HopLimit(hop_limit) => {
                        header.set_hop_limit(hop_limit);
                        model.hop_limit = hop_limit;
                    }
                    Op::Src(src) => {
                        prop_assert_eq!(Ipv6Addr::from(model.src), header.set_src(Ipv6Addr::from(src)));
                        model.src = src;
                    }
                    Op::Dst(dst) => {
                        prop_assert_eq!(Ipv6Addr::from(model.dst), header.set_dst(Ipv6Addr::from(dst)));
                        model.dst = dst;
                    }
                }
            }
            let (header, rest) = Ipv6Header::parse(&buf).unwrap();
            prop_assert_eq!(6, header.version());
            prop_assert_eq!(&model, &Fields::read(header));
            prop_assert_eq!(usize::from(header.payload_len()), payload.len());
            prop_assert_eq!(&payload[..], rest);
        }
    }
}
