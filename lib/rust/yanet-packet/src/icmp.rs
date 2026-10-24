use core::net::Ipv6Addr;

use zerocopy::{FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned, byteorder::network_endian::U16};

use crate::{ParseError, checksum, split_header, split_header_mut};

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

    /// Splits the header off the front of the input.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the input is shorter than the header.
    pub fn parse(buf: &[u8]) -> Result<(&Self, &[u8]), ParseError> {
        split_header(buf)
    }

    /// Splits the header off the front of the input for in-place editing.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the input is shorter than the header.
    pub fn parse_mut(buf: &mut [u8]) -> Result<(&mut Self, &mut [u8]), ParseError> {
        split_header_mut(buf)
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

    /// Splits the header off the front of the input.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the input is shorter than the header.
    pub fn parse(buf: &[u8]) -> Result<(&Self, &[u8]), ParseError> {
        split_header(buf)
    }

    /// Splits the header off the front of the input for in-place editing.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the input is shorter than the header.
    pub fn parse_mut(buf: &mut [u8]) -> Result<(&mut Self, &mut [u8]), ParseError> {
        split_header_mut(buf)
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

    use proptest::{collection::vec, prelude::*};

    use super::*;
    use crate::{IPPROTO_ICMPV6, testing};

    /// An ICMPv4 echo request carrying the payload, and its length.
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
    fn test_icmpv4_parse_reads_fields() {
        let (buf, total) = echo(&[1, 2, 3]);
        let (h, payload) = Icmpv4Header::parse(&buf[..total]).unwrap();
        assert_eq!(8, h.icmp_type());
        assert_eq!(0, h.code());
        assert_eq!([0x12, 0x34, 0, 7], h.rest());
        assert_eq!([1, 2, 3], payload);
    }

    #[test]
    fn test_icmpv4_parse_refuses_short_buffer() {
        let (buf, _) = echo(&[]);
        for len in 0..Icmpv4Header::LEN {
            let expected = ParseError::Truncated { needed: 8, available: len };
            assert_eq!(expected, Icmpv4Header::parse(&buf[..len]).unwrap_err(), "len {len}");
        }
        let mut copy = buf;
        let expected = ParseError::Truncated { needed: 8, available: 7 };
        assert_eq!(expected, Icmpv4Header::parse_mut(&mut copy[..7]).unwrap_err());
    }

    #[test]
    fn test_icmpv6_parse_refuses_short_buffer() {
        let buf = [0u8; 8];
        for len in 0..Icmpv6Header::LEN {
            let expected = ParseError::Truncated { needed: 8, available: len };
            assert_eq!(expected, Icmpv6Header::parse(&buf[..len]).unwrap_err(), "len {len}");
        }
        let mut copy = buf;
        let expected = ParseError::Truncated { needed: 8, available: 7 };
        assert_eq!(expected, Icmpv6Header::parse_mut(&mut copy[..7]).unwrap_err());
        assert!(Icmpv6Header::parse(&buf).is_ok());
    }

    /// ICMP header fields as built and read back; both versions share them.
    #[derive(Debug, Clone, PartialEq, Eq)]
    struct IcmpFields {
        icmp_type: u8,
        code: u8,
        checksum: u16,
        rest: [u8; 4],
    }

    impl IcmpFields {
        fn to_bytes(&self, payload: &[u8]) -> Vec<u8> {
            let checksum = self.checksum.to_be_bytes();
            [&[self.icmp_type, self.code][..], &checksum, &self.rest, payload].concat()
        }
    }

    /// Random ICMP header fields.
    fn icmp_fields() -> impl Strategy<Value = IcmpFields> {
        (any::<u8>(), any::<u8>(), any::<u16>(), any::<[u8; 4]>())
            .prop_map(|(icmp_type, code, checksum, rest)| IcmpFields { icmp_type, code, checksum, rest })
    }

    /// A well-formed ICMP message with a random checksum field.
    fn message() -> impl Strategy<Value = Vec<u8>> {
        (icmp_fields(), vec(any::<u8>(), 0..=24)).prop_map(|(fields, payload)| fields.to_bytes(&payload))
    }

    /// A message whose checksum field is valid without a pseudo header.
    fn checksummed_message() -> impl Strategy<Value = Vec<u8>> {
        message().prop_map(|mut buf| {
            buf[2..4].fill(0);
            let sum = !checksum::sum(&buf);
            buf[2..4].copy_from_slice(&sum.to_be_bytes());
            buf
        })
    }

    /// The fields a header reads back.
    fn read_v4(header: &Icmpv4Header) -> IcmpFields {
        IcmpFields {
            icmp_type: header.icmp_type(),
            code: header.code(),
            checksum: header.checksum(),
            rest: header.rest(),
        }
    }

    /// The fields a header reads back.
    fn read_v6(header: &Icmpv6Header) -> IcmpFields {
        IcmpFields {
            icmp_type: header.icmp_type(),
            code: header.code(),
            checksum: header.checksum(),
            rest: header.rest(),
        }
    }

    /// A checksum-maintaining mutation of an ICMP header.
    #[derive(Debug, Clone)]
    enum IcmpOp {
        TypeCode(u8, u8),
        Rest([u8; 4]),
    }

    /// A mutation of an ICMPv6 message: a header one, or a new pseudo-header
    /// source address.
    #[derive(Debug, Clone)]
    enum Icmpv6Op {
        Header(IcmpOp),
        Src([u8; 16]),
    }

    /// A random header mutation.
    fn icmp_op() -> impl Strategy<Value = IcmpOp> {
        prop_oneof![
            (any::<u8>(), any::<u8>()).prop_map(|(t, c)| IcmpOp::TypeCode(t, c)),
            any::<[u8; 4]>().prop_map(IcmpOp::Rest),
        ]
    }

    /// Random sequences of ICMPv4 mutations.
    fn icmpv4_ops() -> impl Strategy<Value = Vec<IcmpOp>> {
        vec(icmp_op(), 0..=12)
    }

    /// Random sequences of ICMPv6 mutations.
    fn icmpv6_ops() -> impl Strategy<Value = Vec<Icmpv6Op>> {
        let op = prop_oneof![
            2 => icmp_op().prop_map(Icmpv6Op::Header),
            1 => any::<[u8; 16]>().prop_map(Icmpv6Op::Src),
        ];
        vec(op, 0..=12)
    }

    proptest! {
        #![proptest_config(testing::config())]

        #[test]
        fn test_icmpv4_round_trip_reads_back_built_fields(fields in icmp_fields(), payload in vec(any::<u8>(), 0..=24)) {
            let buf = fields.to_bytes(&payload);
            let (header, rest) = Icmpv4Header::parse(&buf).unwrap();
            prop_assert_eq!(&fields, &read_v4(header));
            prop_assert_eq!(&payload[..], rest);
        }

        #[test]
        fn test_icmpv4_setters_change_only_their_field(buf in checksummed_message(), ops in icmpv4_ops()) {
            let mut buf = buf;
            let (header, _) = Icmpv4Header::parse(&buf).unwrap();
            let mut model = read_v4(header);
            for op in ops {
                let (header, _) = Icmpv4Header::parse_mut(&mut buf).unwrap();
                match op {
                    IcmpOp::TypeCode(icmp_type, code) => {
                        header.set_type_code(icmp_type, code);
                        (model.icmp_type, model.code) = (icmp_type, code);
                    }
                    IcmpOp::Rest(rest) => {
                        header.set_rest(rest);
                        model.rest = rest;
                    }
                }
                model.checksum = header.checksum();
                prop_assert_eq!(&model, &read_v4(header));
            }
        }

        #[test]
        fn test_icmpv4_incremental_checksum_equals_full_sum(buf in checksummed_message(), ops in icmpv4_ops()) {
            let mut buf = buf;
            for op in ops {
                let (header, _) = Icmpv4Header::parse_mut(&mut buf).unwrap();
                match op {
                    IcmpOp::TypeCode(icmp_type, code) => header.set_type_code(icmp_type, code),
                    IcmpOp::Rest(rest) => header.set_rest(rest),
                }
            }
            prop_assert_eq!(0xffff, checksum::sum(&buf));
        }

        #[test]
        fn test_icmpv6_round_trip_reads_back_built_fields(fields in icmp_fields(), payload in vec(any::<u8>(), 0..=24)) {
            let buf = fields.to_bytes(&payload);
            let (header, rest) = Icmpv6Header::parse(&buf).unwrap();
            prop_assert_eq!(&fields, &read_v6(header));
            prop_assert_eq!(&payload[..], rest);
        }

        #[test]
        fn test_icmpv6_setters_change_only_their_field(buf in message(), ops in icmpv6_ops()) {
            let mut buf = buf;
            let (header, _) = Icmpv6Header::parse(&buf).unwrap();
            let mut model = read_v6(header);
            let mut src = Ipv6Addr::UNSPECIFIED;
            for op in ops {
                let (header, _) = Icmpv6Header::parse_mut(&mut buf).unwrap();
                match op {
                    Icmpv6Op::Header(IcmpOp::TypeCode(icmp_type, code)) => {
                        header.set_type_code(icmp_type, code);
                        (model.icmp_type, model.code) = (icmp_type, code);
                    }
                    Icmpv6Op::Header(IcmpOp::Rest(rest)) => {
                        header.set_rest(rest);
                        model.rest = rest;
                    }
                    Icmpv6Op::Src(new) => {
                        header.replace_ipv6_addr(src, Ipv6Addr::from(new));
                        src = Ipv6Addr::from(new);
                    }
                }
                model.checksum = header.checksum();
                prop_assert_eq!(&model, &read_v6(header));
            }
        }

        #[test]
        fn test_icmpv6_incremental_checksum_verifies_over_pseudo_header(
            buf in message(),
            src in any::<[u8; 16]>(),
            dst in any::<[u8; 16]>(),
            ops in icmpv6_ops(),
        ) {
            let mut buf = buf;
            buf[2..4].fill(0);
            let sum = checksum::l4_checksum_ipv6(Ipv6Addr::from(src), Ipv6Addr::from(dst), IPPROTO_ICMPV6, &buf);
            buf[2..4].copy_from_slice(&sum.to_be_bytes());
            let mut src = Ipv6Addr::from(src);
            for op in ops {
                let (header, _) = Icmpv6Header::parse_mut(&mut buf).unwrap();
                match op {
                    Icmpv6Op::Header(IcmpOp::TypeCode(icmp_type, code)) => header.set_type_code(icmp_type, code),
                    Icmpv6Op::Header(IcmpOp::Rest(rest)) => header.set_rest(rest),
                    Icmpv6Op::Src(new) => {
                        header.replace_ipv6_addr(src, Ipv6Addr::from(new));
                        src = Ipv6Addr::from(new);
                    }
                }
            }
            prop_assert!(checksum::l4_is_valid_ipv6(src, Ipv6Addr::from(dst), IPPROTO_ICMPV6, &buf));
        }
    }
}
