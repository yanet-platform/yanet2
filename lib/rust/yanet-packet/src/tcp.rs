use core::net::{Ipv4Addr, Ipv6Addr};

use zerocopy::{
    FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned,
    byteorder::network_endian::{U16, U32},
};

use crate::{ParseError, checksum, split_header, split_header_mut};

/// TCP fixed header (20 bytes, options excluded).
///
/// Port and address setters keep the checksum consistent incrementally; the
/// checksum is never stored as zero by an update.
#[derive(Debug, Clone, Copy, PartialEq, Eq, FromBytes, IntoBytes, KnownLayout, Immutable, Unaligned)]
#[repr(C)]
pub struct TcpHeader {
    src_port: U16,
    dst_port: U16,
    seq: U32,
    ack: U32,
    offset_flags: U16,
    window: U16,
    checksum: U16,
    urgent: U16,
}

impl TcpHeader {
    pub const MIN_LEN: usize = 20;
    pub const FIN: u16 = 0x01;
    pub const SYN: u16 = 0x02;
    pub const RST: u16 = 0x04;
    pub const PSH: u16 = 0x08;
    pub const ACK: u16 = 0x10;
    pub const URG: u16 = 0x20;

    pub fn src_port(&self) -> u16 {
        self.src_port.get()
    }

    pub fn dst_port(&self) -> u16 {
        self.dst_port.get()
    }

    pub fn seq(&self) -> u32 {
        self.seq.get()
    }

    pub fn ack(&self) -> u32 {
        self.ack.get()
    }

    /// Header length in 32-bit words.
    pub fn data_offset(&self) -> u8 {
        (self.offset_flags.get() >> 12) as u8
    }

    /// Header length in bytes, options included.
    pub fn header_len(&self) -> usize {
        usize::from(self.data_offset()) * 4
    }

    /// Flag bits, including the nonce bit.
    pub fn flags(&self) -> u16 {
        self.offset_flags.get() & 0x01ff
    }

    pub fn window(&self) -> u16 {
        self.window.get()
    }

    pub fn checksum(&self) -> u16 {
        self.checksum.get()
    }

    pub fn urgent(&self) -> u16 {
        self.urgent.get()
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
    pub fn replace_ipv4_addr(&mut self, old: Ipv4Addr, new: Ipv4Addr) {
        self.checksum.set(checksum::update_bytes_no_zero(
            self.checksum.get(),
            &old.octets(),
            &new.octets(),
        ));
    }

    /// Fixes the checksum after the IPv6 source or destination changed.
    pub fn replace_ipv6_addr(&mut self, old: Ipv6Addr, new: Ipv6Addr) {
        self.checksum.set(checksum::update_bytes_no_zero(
            self.checksum.get(),
            &old.octets(),
            &new.octets(),
        ));
    }

    fn update_word(&mut self, old: u16, new: u16) {
        self.checksum
            .set(checksum::update_no_zero(self.checksum.get(), old, new));
    }
}

fn split_options(header: &TcpHeader, available: usize) -> Result<usize, ParseError> {
    if header.data_offset() < 5 {
        return Err(ParseError::HeaderLen { words: header.data_offset() });
    }
    let header_len = header.header_len();
    if header_len > available {
        return Err(ParseError::Truncated { needed: header_len, available });
    }
    Ok(header_len - TcpHeader::MIN_LEN)
}

/// Read-only TCP segment split into header, options and payload.
#[derive(Debug, Clone, Copy)]
#[non_exhaustive]
pub struct TcpPacket<'a> {
    pub header: &'a TcpHeader,
    pub options: &'a [u8],
    pub payload: &'a [u8],
}

impl<'a> TcpPacket<'a> {
    /// Splits a TCP segment at the start of the input.
    ///
    /// Checks the fixed header, the data offset and the options, in this
    /// order; the first failure is reported.
    ///
    /// # Errors
    ///
    /// - [`ParseError::Truncated`] when the input ends before the fixed header,
    ///   or before the options the data offset counts.
    /// - [`ParseError::HeaderLen`] when the data offset is below five words.
    pub fn parse(buf: &'a [u8]) -> Result<Self, ParseError> {
        let available = buf.len();
        let (header, rest) = split_header::<TcpHeader>(buf)?;
        let options_len = split_options(header, available)?;
        // The options end within the input, so the split cannot panic.
        let (options, payload) = rest.split_at(options_len);
        Ok(Self { header, options, payload })
    }
}

/// Mutable TCP segment split into header, options and payload.
#[derive(Debug)]
#[non_exhaustive]
pub struct TcpPacketMut<'a> {
    pub header: &'a mut TcpHeader,
    pub options: &'a mut [u8],
    pub payload: &'a mut [u8],
}

impl<'a> TcpPacketMut<'a> {
    /// Same as [`TcpPacket::parse`] for in-place editing.
    ///
    /// # Errors
    ///
    /// The same as [`TcpPacket::parse`].
    pub fn parse(buf: &'a mut [u8]) -> Result<Self, ParseError> {
        let available = buf.len();
        let (header, rest) = split_header_mut::<TcpHeader>(buf)?;
        let options_len = split_options(header, available)?;
        // The options end within the input, so the split cannot panic.
        let (options, payload) = rest.split_at_mut(options_len);
        Ok(Self { header, options, payload })
    }
}

#[cfg(test)]
mod tests {

    use proptest::{collection::vec, prelude::*};

    use super::*;
    use crate::{
        IPPROTO_TCP,
        testing::{self, L4Op},
    };

    /// Builds a segment with the given number of option words and a valid
    /// checksum for the given IPv4 addresses; returns the buffer and the
    /// segment length.
    fn build(src: [u8; 4], dst: [u8; 4], opts: usize, payload: &[u8]) -> ([u8; 96], usize) {
        let mut buf = [0u8; 96];
        let hlen = 20 + opts * 4;
        let total = hlen + payload.len();
        buf[0..2].copy_from_slice(&40000u16.to_be_bytes());
        buf[2..4].copy_from_slice(&443u16.to_be_bytes());
        buf[4..8].copy_from_slice(&0x01020304u32.to_be_bytes());
        buf[8..12].copy_from_slice(&0x0a0b0c0du32.to_be_bytes());
        buf[12] = ((5 + opts) as u8) << 4;
        buf[13] = 0x12;
        buf[14..16].copy_from_slice(&0x2000u16.to_be_bytes());
        for (i, b) in buf[20..hlen].iter_mut().enumerate() {
            *b = (i as u8).wrapping_mul(11).wrapping_add(3);
        }
        buf[hlen..total].copy_from_slice(payload);
        let sum = checksum::l4_checksum_ipv4(Ipv4Addr::from(src), Ipv4Addr::from(dst), IPPROTO_TCP, &buf[..total]);
        buf[16..18].copy_from_slice(&sum.to_be_bytes());
        (buf, total)
    }

    #[test]
    fn test_tcp_parse_fields() {
        let (buf, total) = build([1, 2, 3, 4], [5, 6, 7, 8], 1, &[7, 7, 7]);
        let p = TcpPacket::parse(&buf[..total]).unwrap();
        assert_eq!(40000, p.header.src_port());
        assert_eq!(443, p.header.dst_port());
        assert_eq!(0x01020304, p.header.seq());
        assert_eq!(0x0a0b0c0d, p.header.ack());
        assert_eq!(6, p.header.data_offset());
        assert_eq!(24, p.header.header_len());
        assert_eq!(TcpHeader::SYN | TcpHeader::ACK, p.header.flags());
        assert_eq!(0x2000, p.header.window());
        assert_eq!(0, p.header.urgent());
        assert_eq!(4, p.options.len());
        assert_eq!([7, 7, 7], p.payload);
    }

    #[test]
    fn test_tcp_refuses_short_buffer() {
        let (buf, _) = build([1, 2, 3, 4], [5, 6, 7, 8], 2, &[]);
        for len in 0..TcpHeader::MIN_LEN {
            let expected = ParseError::Truncated { needed: 20, available: len };
            assert_eq!(expected, TcpPacket::parse(&buf[..len]).unwrap_err(), "len {len}");
        }
        let mut copy = buf;
        let expected = ParseError::Truncated { needed: 20, available: 19 };
        assert_eq!(expected, TcpPacketMut::parse(&mut copy[..19]).unwrap_err());
    }

    #[test]
    fn test_tcp_refuses_options_beyond_input() {
        let (mut buf, total) = build([1, 2, 3, 4], [5, 6, 7, 8], 2, &[]);
        for len in TcpHeader::MIN_LEN..total {
            let expected = ParseError::Truncated { needed: 28, available: len };
            assert_eq!(expected, TcpPacket::parse(&buf[..len]).unwrap_err(), "len {len}");
        }
        let expected = ParseError::Truncated { needed: 28, available: 27 };
        assert_eq!(expected, TcpPacketMut::parse(&mut buf[..27]).unwrap_err());
    }

    #[test]
    fn test_tcp_refuses_data_offset_below_five_words() {
        let (mut buf, total) = build([1, 2, 3, 4], [5, 6, 7, 8], 2, &[]);
        for offset in 0..5u8 {
            buf[12] = offset << 4;
            let expected = ParseError::HeaderLen { words: offset };
            assert_eq!(
                expected,
                TcpPacket::parse(&buf[..total]).unwrap_err(),
                "offset {offset}"
            );
            assert_eq!(expected, TcpPacketMut::parse(&mut buf[..total]).unwrap_err());
        }
    }

    #[test]
    fn test_tcp_refuses_data_offset_beyond_input() {
        let (mut buf, total) = build([1, 2, 3, 4], [5, 6, 7, 8], 2, &[]);
        buf[12] = 0xf0;
        let expected = ParseError::Truncated { needed: 60, available: total };
        assert_eq!(expected, TcpPacket::parse(&buf[..total]).unwrap_err());
        assert_eq!(expected, TcpPacketMut::parse(&mut buf[..total]).unwrap_err());
    }

    #[test]
    fn test_tcp_segment_without_options_needs_twenty_bytes() {
        let (buf, _) = build([1, 2, 3, 4], [5, 6, 7, 8], 0, &[]);
        assert!(TcpPacket::parse(&buf[..20]).is_ok());
        let expected = ParseError::Truncated { needed: 20, available: 19 };
        assert_eq!(expected, TcpPacket::parse(&buf[..19]).unwrap_err());
    }

    /// TCP header fields as built and read back; the data offset follows the
    /// options.
    #[derive(Debug, Clone, PartialEq, Eq)]
    struct Fields {
        src_port: u16,
        dst_port: u16,
        seq: u32,
        ack: u32,
        flags: u16,
        window: u16,
        checksum: u16,
        urgent: u16,
        options: Vec<u8>,
    }

    impl Fields {
        fn to_bytes(&self, payload: &[u8]) -> Vec<u8> {
            let offset_flags = ((5 + self.options.len() / 4) as u16) << 12 | self.flags;
            [
                &self.src_port.to_be_bytes()[..],
                &self.dst_port.to_be_bytes(),
                &self.seq.to_be_bytes(),
                &self.ack.to_be_bytes(),
                &offset_flags.to_be_bytes(),
                &self.window.to_be_bytes(),
                &self.checksum.to_be_bytes(),
                &self.urgent.to_be_bytes(),
                &self.options,
                payload,
            ]
            .concat()
        }

        fn read(packet: &TcpPacket) -> Self {
            let header = packet.header;
            Self {
                src_port: header.src_port(),
                dst_port: header.dst_port(),
                seq: header.seq(),
                ack: header.ack(),
                flags: header.flags(),
                window: header.window(),
                checksum: header.checksum(),
                urgent: header.urgent(),
                options: packet.options.to_vec(),
            }
        }
    }

    /// Random TCP header fields with up to ten option words.
    fn fields() -> impl Strategy<Value = Fields> {
        (
            any::<(u16, u16)>(),
            any::<(u32, u32)>(),
            0u16..0x200,
            any::<(u16, u16, u16)>(),
            vec(any::<[u8; 4]>(), 0..=10),
        )
            .prop_map(
                |((src_port, dst_port), (seq, ack), flags, (window, checksum, urgent), options)| Fields {
                    src_port,
                    dst_port,
                    seq,
                    ack,
                    flags,
                    window,
                    checksum,
                    urgent,
                    options: options.concat(),
                },
            )
    }

    /// Offsets the property inputs may lie at: the data offset byte.
    const LIES: &[usize] = &[12];

    /// A well-formed segment with a random checksum field.
    fn segment() -> impl Strategy<Value = Vec<u8>> {
        (fields(), vec(any::<u8>(), 0..=32)).prop_map(|(fields, payload)| fields.to_bytes(&payload))
    }

    /// The header and payload parts of a parse, in input order, or its error.
    fn parts(buf: &[u8]) -> Result<Vec<&[u8]>, ParseError> {
        let packet = TcpPacket::parse(buf)?;
        Ok(vec![packet.header.as_bytes(), packet.options, packet.payload])
    }

    /// The parts as above, through the mutable parse.
    fn parts_mut(buf: &mut [u8]) -> Result<Vec<&[u8]>, ParseError> {
        let TcpPacketMut { header, options, payload } = TcpPacketMut::parse(buf)?;
        Ok(vec![header.as_bytes(), &*options, &*payload])
    }

    impl testing::L4Header for TcpHeader {
        fn set_src_port(&mut self, port: u16) {
            TcpHeader::set_src_port(self, port);
        }

        fn set_dst_port(&mut self, port: u16) {
            TcpHeader::set_dst_port(self, port);
        }

        fn replace_ipv4_addr(&mut self, old: Ipv4Addr, new: Ipv4Addr) {
            TcpHeader::replace_ipv4_addr(self, old, new);
        }

        fn replace_ipv6_addr(&mut self, old: Ipv6Addr, new: Ipv6Addr) {
            TcpHeader::replace_ipv6_addr(self, old, new);
        }
    }

    /// Asserts that the error carries the values the input bytes hold.
    fn assert_error(buf: &[u8], error: ParseError) {
        match error {
            ParseError::Truncated { needed, available } => {
                assert_eq!(buf.len(), available);
                assert!(needed > available);
                if buf.len() < TcpHeader::MIN_LEN {
                    assert_eq!(TcpHeader::MIN_LEN, needed);
                } else {
                    assert_eq!(usize::from(buf[12] >> 4) * 4, needed);
                }
            }
            ParseError::HeaderLen { words } => {
                assert_eq!(buf[12] >> 4, words);
                assert!(words < 5);
            }
            ParseError::Version { .. }
            | ParseError::LengthBelowHeader { .. }
            | ParseError::LengthBeyondInput { .. } => {
                panic!("unexpected {error:?}")
            }
        }
    }

    proptest! {
        #![proptest_config(testing::config())]

        #[test]
        fn test_tcp_parse_ok_respects_bounds(buf in testing::input(segment(), LIES)) {
            if let Ok(parts) = parts(&buf) {
                testing::assert_adjacent(&buf, &parts);
                let header = TcpHeader::read_from_bytes(parts[0]).unwrap();
                prop_assert_eq!(TcpHeader::MIN_LEN, parts[0].len());
                prop_assert_eq!(header.header_len(), parts[0].len() + parts[1].len());
                prop_assert_eq!(buf.len(), header.header_len() + parts[2].len());
            }
        }

        #[test]
        fn test_tcp_parse_error_reports_fields_of_input(buf in testing::input(segment(), LIES)) {
            if let Err(error) = parts(&buf) {
                assert_error(&buf, error);
            }
        }

        #[test]
        fn test_tcp_parse_and_parse_mut_agree(buf in testing::input(segment(), LIES)) {
            let mut copy = buf.clone();
            prop_assert_eq!(testing::lens(&parts(&buf)), testing::lens(&parts_mut(&mut copy)));
        }

        #[test]
        fn test_tcp_round_trip_reads_back_built_fields(fields in fields(), payload in vec(any::<u8>(), 0..=32)) {
            let buf = fields.to_bytes(&payload);
            let packet = TcpPacket::parse(&buf).unwrap();
            prop_assert_eq!(&fields, &Fields::read(&packet));
            prop_assert_eq!(5 + fields.options.len() / 4, usize::from(packet.header.data_offset()));
            prop_assert_eq!(&payload[..], packet.payload);
        }

        #[test]
        fn test_tcp_setters_change_only_their_field(
            start in fields(),
            payload in vec(any::<u8>(), 0..=32),
            family in testing::family(),
            addrs in any::<([u8; 16], [u8; 16])>(),
            ops in testing::l4_ops(),
        ) {
            let mut buf = start.to_bytes(&payload);
            let mut model = start.clone();
            let mut addrs = addrs;
            for op in &ops {
                let packet = TcpPacketMut::parse(&mut buf).unwrap();
                testing::apply(packet.header, family, op, &mut addrs);
                match *op {
                    L4Op::SrcPort(port) => model.src_port = port,
                    L4Op::DstPort(port) => model.dst_port = port,
                    L4Op::Src(_) | L4Op::Dst(_) => {}
                }
                model.checksum = packet.header.checksum();
            }
            let packet = TcpPacket::parse(&buf).unwrap();
            prop_assert_eq!(&model, &Fields::read(&packet));
            prop_assert_eq!(&payload[..], packet.payload);
        }

        #[test]
        fn test_tcp_incremental_checksum_verifies_over_pseudo_header(
            start in fields(),
            payload in vec(any::<u8>(), 0..=32),
            family in testing::family(),
            addrs in any::<([u8; 16], [u8; 16])>(),
            ops in testing::l4_ops(),
        ) {
            let start = Fields { checksum: 0, ..start };
            let mut buf = start.to_bytes(&payload);
            let sum = family.l4_checksum(addrs.0, addrs.1, IPPROTO_TCP, &buf);
            buf[16..18].copy_from_slice(&sum.to_be_bytes());
            let mut addrs = addrs;
            for op in &ops {
                let packet = TcpPacketMut::parse(&mut buf).unwrap();
                testing::apply(packet.header, family, op, &mut addrs);
                prop_assert_ne!(0, packet.header.checksum());
            }
            prop_assert!(family.l4_is_valid(addrs.0, addrs.1, IPPROTO_TCP, &buf));
        }
    }
}
