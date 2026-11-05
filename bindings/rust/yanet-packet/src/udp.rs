use core::net::{Ipv4Addr, Ipv6Addr};

use zerocopy::{FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned, byteorder::network_endian::U16};

use crate::{ParseError, checksum, split_header, split_header_mut};

/// UDP header.
///
/// Updates never store a zero checksum, which would mean "no checksum".
#[derive(Debug, Clone, Copy, PartialEq, Eq, FromBytes, IntoBytes, KnownLayout, Immutable, Unaligned)]
#[repr(C)]
pub struct UdpHeader {
    src_port: U16,
    dst_port: U16,
    length: U16,
    checksum: U16,
}

impl UdpHeader {
    pub const LEN: usize = 8;

    /// Splits the header and its payload off the front of the input.
    ///
    /// The payload ends at the length field.
    ///
    /// # Errors
    ///
    /// - [`ParseError::Truncated`] when the input is shorter than the header.
    /// - [`ParseError::LengthBelowHeader`] when the length field is below the
    ///   header size.
    /// - [`ParseError::LengthBeyondInput`] when the length field is beyond the
    ///   input.
    pub fn parse(buf: &[u8]) -> Result<(&Self, &[u8]), ParseError> {
        let available = buf.len();
        let (header, rest) = split_header::<Self>(buf)?;
        let payload_len = header.payload_len(available)?;
        Ok((header, &rest[..payload_len]))
    }

    /// Same as [`UdpHeader::parse`] for in-place editing.
    ///
    /// # Errors
    ///
    /// The same as [`UdpHeader::parse`].
    pub fn parse_mut(buf: &mut [u8]) -> Result<(&mut Self, &mut [u8]), ParseError> {
        let available = buf.len();
        let (header, rest) = split_header_mut::<Self>(buf)?;
        let payload_len = header.payload_len(available)?;
        Ok((header, &mut rest[..payload_len]))
    }

    /// Payload length from the length field, checked against the input.
    fn payload_len(&self, available: usize) -> Result<usize, ParseError> {
        let length = usize::from(self.length.get());
        if length < Self::LEN {
            return Err(ParseError::LengthBelowHeader { length, header_len: Self::LEN });
        }
        if length > available {
            return Err(ParseError::LengthBeyondInput { length, available });
        }
        Ok(length - Self::LEN)
    }

    pub fn src_port(&self) -> u16 {
        self.src_port.get()
    }

    pub fn dst_port(&self) -> u16 {
        self.dst_port.get()
    }

    /// Length of header and payload.
    pub fn length(&self) -> u16 {
        self.length.get()
    }

    pub fn checksum(&self) -> u16 {
        self.checksum.get()
    }

    /// True when the sender skipped the checksum, legal over IPv4 and, for
    /// tunnels, over IPv6 (RFC 6935).
    pub fn has_no_checksum(&self) -> bool {
        self.checksum.get() == 0
    }

    pub fn set_src_port(&mut self, port: u16) {
        let old = self.src_port.get();
        self.src_port.set(port);
        self.update_word(old, port);
    }

    pub fn set_dst_port(&mut self, port: u16) {
        let old = self.dst_port.get();
        self.dst_port.set(port);
        self.update_word(old, port);
    }

    /// Fixes the checksum after the IPv4 source or destination changed.
    ///
    /// A zero checksum over IPv4 means no checksum and is left alone.
    pub fn replace_ipv4_addr(&mut self, old: Ipv4Addr, new: Ipv4Addr) {
        if self.has_no_checksum() {
            return;
        }
        self.checksum.set(checksum::update_bytes_no_zero(
            self.checksum.get(),
            &old.octets(),
            &new.octets(),
        ));
    }

    /// Fixes the checksum after the IPv6 source or destination changed.
    ///
    /// A zero checksum, a tunnel's no checksum, is left alone as on IPv4.
    pub fn replace_ipv6_addr(&mut self, old: Ipv6Addr, new: Ipv6Addr) {
        if self.has_no_checksum() {
            return;
        }
        self.checksum.set(checksum::update_bytes_no_zero(
            self.checksum.get(),
            &old.octets(),
            &new.octets(),
        ));
    }

    fn update_word(&mut self, old: u16, new: u16) {
        if self.has_no_checksum() {
            return;
        }
        self.checksum
            .set(checksum::update_no_zero(self.checksum.get(), old, new));
    }
}

#[cfg(test)]
mod tests {

    use proptest::{collection::vec, prelude::*};

    use super::*;
    use crate::{
        IPPROTO_UDP,
        testing::{self, L4Op},
    };

    /// A UDP datagram over IPv4 carrying the payload, with its checksum set or
    /// zero, and its length.
    fn build(src: [u8; 4], dst: [u8; 4], payload: &[u8], with_checksum: bool) -> ([u8; 64], usize) {
        let mut buf = [0u8; 64];
        let total = 8 + payload.len();
        buf[0..2].copy_from_slice(&5353u16.to_be_bytes());
        buf[2..4].copy_from_slice(&53u16.to_be_bytes());
        buf[4..6].copy_from_slice(&(total as u16).to_be_bytes());
        buf[8..total].copy_from_slice(payload);
        if with_checksum {
            let sum = checksum::l4_checksum_ipv4(Ipv4Addr::from(src), Ipv4Addr::from(dst), IPPROTO_UDP, &buf[..total]);
            let sum = if sum == 0 { 0xffff } else { sum };
            buf[6..8].copy_from_slice(&sum.to_be_bytes());
        }
        (buf, total)
    }

    #[test]
    fn test_udp_parse_fields() {
        let (buf, total) = build([1, 1, 1, 1], [2, 2, 2, 2], &[9, 8, 7], true);
        let (h, payload) = UdpHeader::parse(&buf[..total + 4]).unwrap();
        assert_eq!(5353, h.src_port());
        assert_eq!(53, h.dst_port());
        assert_eq!(11, h.length());
        assert_ne!(0, h.checksum());
        assert_eq!([9, 8, 7], payload);
    }

    #[test]
    fn test_udp_refuses_short_buffer() {
        let (buf, _) = build([1, 1, 1, 1], [2, 2, 2, 2], &[9, 8, 7], true);
        for len in 0..UdpHeader::LEN {
            let expected = ParseError::Truncated { needed: 8, available: len };
            assert_eq!(expected, UdpHeader::parse(&buf[..len]).unwrap_err(), "len {len}");
        }
        let mut copy = buf;
        let expected = ParseError::Truncated { needed: 8, available: 7 };
        assert_eq!(expected, UdpHeader::parse_mut(&mut copy[..7]).unwrap_err());
    }

    #[test]
    fn test_udp_refuses_length_beyond_input() {
        let (mut buf, total) = build([1, 1, 1, 1], [2, 2, 2, 2], &[9, 8, 7], true);
        for len in UdpHeader::LEN..total {
            let expected = ParseError::LengthBeyondInput { length: 11, available: len };
            assert_eq!(expected, UdpHeader::parse(&buf[..len]).unwrap_err(), "len {len}");
        }
        let expected = ParseError::LengthBeyondInput { length: 11, available: 10 };
        assert_eq!(expected, UdpHeader::parse_mut(&mut buf[..10]).unwrap_err());
    }

    #[test]
    fn test_udp_trailing_bytes_are_not_payload() {
        let (buf, total) = build([1, 1, 1, 1], [2, 2, 2, 2], &[9, 8, 7], true);
        let (_, payload) = UdpHeader::parse(&buf[..total + 5]).unwrap();
        assert_eq!(3, payload.len());
    }

    #[test]
    fn test_udp_refuses_length_below_header() {
        let (mut buf, total) = build([1, 1, 1, 1], [2, 2, 2, 2], &[9, 8, 7], true);
        for length in 0..8u8 {
            buf[5] = length;
            let expected = ParseError::LengthBelowHeader {
                length: usize::from(length),
                header_len: 8,
            };
            assert_eq!(
                expected,
                UdpHeader::parse(&buf[..total]).unwrap_err(),
                "length {length}"
            );
            assert_eq!(expected, UdpHeader::parse_mut(&mut buf[..total]).unwrap_err());
        }
    }

    #[test]
    fn test_udp_zero_checksum_over_ipv4_is_preserved() {
        let (mut buf, total) = build([1, 1, 1, 1], [2, 2, 2, 2], &[1, 2], false);
        let (h, _) = UdpHeader::parse_mut(&mut buf[..total]).unwrap();
        h.set_src_port(1);
        h.replace_ipv4_addr(Ipv4Addr::new(1, 1, 1, 1), Ipv4Addr::new(9, 9, 9, 9));
        assert_eq!(0, h.checksum());
    }

    #[test]
    fn test_udp_zero_checksum_over_ipv6_is_preserved() {
        let (mut buf, total) = build([1, 1, 1, 1], [2, 2, 2, 2], &[1, 2], false);
        let (h, _) = UdpHeader::parse_mut(&mut buf[..total]).unwrap();
        h.set_src_port(1);
        h.replace_ipv6_addr(Ipv6Addr::LOCALHOST, Ipv6Addr::UNSPECIFIED);
        assert_eq!(0, h.checksum());
    }

    #[test]
    fn test_udp_update_never_yields_zero_checksum() {
        let mut h = UdpHeader::read_from_bytes(&[0; 8]).unwrap();
        // Inner sum all-ones would store zero; it must be stored as all-ones.
        h.checksum.set(0x0001);
        h.replace_ipv4_addr(Ipv4Addr::new(0, 0, 0, 0), Ipv4Addr::new(0, 0, 0, 1));
        assert_eq!(0xffff, h.checksum());
        h.replace_ipv6_addr(Ipv6Addr::UNSPECIFIED, Ipv6Addr::UNSPECIFIED);
        assert_eq!(0xffff, h.checksum());
    }

    /// UDP header fields as built and read back; the length follows the
    /// payload.
    #[derive(Debug, Clone, PartialEq, Eq)]
    struct Fields {
        src_port: u16,
        dst_port: u16,
        checksum: u16,
    }

    impl Fields {
        fn to_bytes(&self, payload: &[u8]) -> Vec<u8> {
            let length = (UdpHeader::LEN + payload.len()) as u16;
            [
                &self.src_port.to_be_bytes()[..],
                &self.dst_port.to_be_bytes(),
                &length.to_be_bytes(),
                &self.checksum.to_be_bytes(),
                payload,
            ]
            .concat()
        }

        fn read(header: &UdpHeader) -> Self {
            Self {
                src_port: header.src_port(),
                dst_port: header.dst_port(),
                checksum: header.checksum(),
            }
        }
    }

    /// Random UDP header fields.
    fn fields() -> impl Strategy<Value = Fields> {
        (any::<u16>(), any::<u16>(), any::<u16>()).prop_map(|(src_port, dst_port, checksum)| Fields {
            src_port,
            dst_port,
            checksum,
        })
    }

    /// Offsets the property inputs may lie at: both length bytes.
    const LIES: &[usize] = &[4, 5];

    /// A well-formed datagram with a random checksum field.
    fn datagram() -> impl Strategy<Value = Vec<u8>> {
        (fields(), vec(any::<u8>(), 0..=24)).prop_map(|(fields, payload)| fields.to_bytes(&payload))
    }

    /// The header and payload parts of a parse, in input order, or its error.
    fn parts(buf: &[u8]) -> Result<Vec<&[u8]>, ParseError> {
        let (header, payload) = UdpHeader::parse(buf)?;
        Ok(vec![header.as_bytes(), payload])
    }

    /// The parts as above, through the mutable parse.
    fn parts_mut(buf: &mut [u8]) -> Result<Vec<&[u8]>, ParseError> {
        let (header, payload) = UdpHeader::parse_mut(buf)?;
        Ok(vec![header.as_bytes(), &*payload])
    }

    impl testing::L4Header for UdpHeader {
        fn set_src_port(&mut self, port: u16) {
            UdpHeader::set_src_port(self, port);
        }

        fn set_dst_port(&mut self, port: u16) {
            UdpHeader::set_dst_port(self, port);
        }

        fn replace_ipv4_addr(&mut self, old: Ipv4Addr, new: Ipv4Addr) {
            UdpHeader::replace_ipv4_addr(self, old, new);
        }

        fn replace_ipv6_addr(&mut self, old: Ipv6Addr, new: Ipv6Addr) {
            UdpHeader::replace_ipv6_addr(self, old, new);
        }
    }

    /// Asserts that the error carries the values the input bytes hold.
    fn assert_error(buf: &[u8], error: ParseError) {
        match error {
            ParseError::Truncated { needed, available } => {
                assert_eq!(UdpHeader::LEN, needed);
                assert_eq!(buf.len(), available);
                assert!(needed > available);
            }
            ParseError::LengthBelowHeader { length, header_len } => {
                assert_eq!(testing::be16(&buf[4..]), length);
                assert_eq!(UdpHeader::LEN, header_len);
                assert!(length < header_len);
            }
            ParseError::LengthBeyondInput { length, available } => {
                assert_eq!(testing::be16(&buf[4..]), length);
                assert_eq!(buf.len(), available);
                assert!(length > available);
            }
            ParseError::Version { .. } | ParseError::HeaderLen { .. } => panic!("unexpected {error:?}"),
        }
    }

    proptest! {
        #![proptest_config(testing::config())]

        #[test]
        fn test_udp_parse_ok_respects_bounds(buf in testing::input(datagram(), LIES)) {
            if let Ok(parts) = parts(&buf) {
                testing::assert_adjacent(&buf, &parts);
                let header = UdpHeader::read_from_bytes(parts[0]).unwrap();
                prop_assert_eq!(UdpHeader::LEN, parts[0].len());
                prop_assert_eq!(usize::from(header.length()), parts[0].len() + parts[1].len());
                prop_assert!(usize::from(header.length()) <= buf.len());
            }
        }

        #[test]
        fn test_udp_parse_error_reports_fields_of_input(buf in testing::input(datagram(), LIES)) {
            if let Err(error) = parts(&buf) {
                assert_error(&buf, error);
            }
        }

        #[test]
        fn test_udp_parse_and_parse_mut_agree(buf in testing::input(datagram(), LIES)) {
            let mut copy = buf.clone();
            prop_assert_eq!(testing::lens(&parts(&buf)), testing::lens(&parts_mut(&mut copy)));
        }

        #[test]
        fn test_udp_round_trip_reads_back_built_fields(fields in fields(), payload in vec(any::<u8>(), 0..=24)) {
            let buf = fields.to_bytes(&payload);
            let (header, rest) = UdpHeader::parse(&buf).unwrap();
            prop_assert_eq!(&fields, &Fields::read(header));
            prop_assert_eq!(payload.len() + UdpHeader::LEN, usize::from(header.length()));
            prop_assert_eq!(&payload[..], rest);
        }

        #[test]
        fn test_udp_setters_change_only_their_field(
            start in fields(),
            payload in vec(any::<u8>(), 0..=24),
            family in testing::family(),
            addrs in any::<([u8; 16], [u8; 16])>(),
            ops in testing::l4_ops(),
        ) {
            let mut buf = start.to_bytes(&payload);
            let mut model = start.clone();
            let mut addrs = addrs;
            for op in &ops {
                let (header, _) = UdpHeader::parse_mut(&mut buf).unwrap();
                testing::apply(header, family, op, &mut addrs);
                match *op {
                    L4Op::SrcPort(port) => model.src_port = port,
                    L4Op::DstPort(port) => model.dst_port = port,
                    L4Op::Src(_) | L4Op::Dst(_) => {}
                }
                model.checksum = header.checksum();
                prop_assert_eq!(&model, &Fields::read(header));
            }
            let (header, rest) = UdpHeader::parse(&buf).unwrap();
            prop_assert_eq!(payload.len() + UdpHeader::LEN, usize::from(header.length()));
            prop_assert_eq!(&payload[..], rest);
        }

        #[test]
        fn test_udp_incremental_checksum_verifies_over_pseudo_header(
            (src_port, dst_port) in any::<(u16, u16)>(),
            payload in vec(any::<u8>(), 0..=24),
            family in testing::family(),
            addrs in any::<([u8; 16], [u8; 16])>(),
            has_checksum in any::<bool>(),
            ops in testing::l4_ops(),
        ) {
            let start = Fields { src_port, dst_port, checksum: 0 };
            let mut buf = start.to_bytes(&payload);
            if has_checksum {
                let sum = family.l4_checksum(addrs.0, addrs.1, IPPROTO_UDP, &buf);
                buf[6..8].copy_from_slice(&if sum == 0 { 0xffff } else { sum }.to_be_bytes());
            }
            let mut addrs = addrs;
            for op in &ops {
                let (header, _) = UdpHeader::parse_mut(&mut buf).unwrap();
                testing::apply(header, family, op, &mut addrs);
                prop_assert_eq!(has_checksum, !header.has_no_checksum());
            }
            if has_checksum {
                prop_assert!(family.l4_is_valid(addrs.0, addrs.1, IPPROTO_UDP, &buf));
            }
        }
    }
}
