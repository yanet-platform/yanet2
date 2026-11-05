use core::net::Ipv4Addr;

use zerocopy::{FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned, byteorder::network_endian::U16};

use crate::{ParseError, checksum, split_header, split_header_mut};

/// IPv4 fixed header (20 bytes, options excluded).
///
/// Every field is covered by the header checksum, so each setter updates it
/// incrementally. A rewritten address must also be reported to the L4 header
/// to fix its pseudo-header checksum.
#[derive(Debug, Clone, Copy, PartialEq, Eq, FromBytes, IntoBytes, KnownLayout, Immutable, Unaligned)]
#[repr(C)]
pub struct Ipv4Header {
    version_ihl: u8,
    tos: u8,
    total_len: U16,
    id: U16,
    frag: U16,
    ttl: u8,
    protocol: u8,
    checksum: U16,
    src: [u8; 4],
    dst: [u8; 4],
}

impl Ipv4Header {
    pub const MIN_LEN: usize = 20;

    pub fn version(&self) -> u8 {
        self.version_ihl >> 4
    }

    /// Header length in 32-bit words.
    pub fn ihl(&self) -> u8 {
        self.version_ihl & 0x0f
    }

    /// Header length in bytes, options included.
    pub fn header_len(&self) -> usize {
        usize::from(self.ihl()) * 4
    }

    pub fn dscp(&self) -> u8 {
        self.tos >> 2
    }

    pub fn ecn(&self) -> u8 {
        self.tos & 3
    }

    pub fn total_len(&self) -> u16 {
        self.total_len.get()
    }

    pub fn id(&self) -> u16 {
        self.id.get()
    }

    /// Flags and fragment offset word.
    pub fn frag(&self) -> u16 {
        self.frag.get()
    }

    /// True for a non-first fragment or a fragment with more to come.
    pub fn is_fragment(&self) -> bool {
        self.frag.get() & 0x3fff != 0
    }

    pub fn ttl(&self) -> u8 {
        self.ttl
    }

    pub fn protocol(&self) -> u8 {
        self.protocol
    }

    pub fn checksum(&self) -> u16 {
        self.checksum.get()
    }

    pub fn src(&self) -> Ipv4Addr {
        Ipv4Addr::from(self.src)
    }

    pub fn dst(&self) -> Ipv4Addr {
        Ipv4Addr::from(self.dst)
    }

    fn tos_word(&self) -> u16 {
        u16::from_be_bytes([self.version_ihl, self.tos])
    }

    fn ttl_word(&self) -> u16 {
        u16::from_be_bytes([self.ttl, self.protocol])
    }

    fn replace_checksum(&mut self, old: u16, new: u16) {
        self.checksum.set(checksum::update(self.checksum.get(), old, new));
    }

    /// Sets the DSCP; the bits above six are dropped and ECN is kept.
    pub fn set_dscp(&mut self, dscp: u8) {
        let old = self.tos_word();
        self.tos = ((dscp & 0x3f) << 2) | (self.tos & 3);
        self.replace_checksum(old, self.tos_word());
    }

    /// Sets the ECN codepoint; the bits above two are dropped.
    pub fn set_ecn(&mut self, ecn: u8) {
        let old = self.tos_word();
        self.tos = (self.tos & 0xfc) | (ecn & 3);
        self.replace_checksum(old, self.tos_word());
    }

    /// Sets the total length and updates the checksum.
    ///
    /// A packet view keeps the split it was parsed with: after a length
    /// change, parse the packet again to see the new payload.
    pub fn set_total_len(&mut self, total_len: u16) {
        let old = self.total_len.get();
        self.total_len.set(total_len);
        self.replace_checksum(old, total_len);
    }

    pub fn set_id(&mut self, id: u16) {
        let old = self.id.get();
        self.id.set(id);
        self.replace_checksum(old, id);
    }

    pub fn set_ttl(&mut self, ttl: u8) {
        let old = self.ttl_word();
        self.ttl = ttl;
        self.replace_checksum(old, self.ttl_word());
    }

    pub fn set_protocol(&mut self, protocol: u8) {
        let old = self.ttl_word();
        self.protocol = protocol;
        self.replace_checksum(old, self.ttl_word());
    }

    /// Decrements the TTL and returns the new value.
    ///
    /// Returns `None` and leaves the header untouched when the TTL is already
    /// 1 or 0, i.e. the packet must not be forwarded.
    #[must_use = "a packet whose TTL cannot be decremented must not be forwarded"]
    pub fn decrement_ttl(&mut self) -> Option<u8> {
        if self.ttl <= 1 {
            return None;
        }
        self.set_ttl(self.ttl - 1);
        Some(self.ttl)
    }

    /// Replaces the source address and returns the previous one.
    pub fn set_src(&mut self, addr: Ipv4Addr) -> Ipv4Addr {
        let old = core::mem::replace(&mut self.src, addr.octets());
        self.checksum
            .set(checksum::update_bytes(self.checksum.get(), &old, &self.src));
        Ipv4Addr::from(old)
    }

    /// Replaces the destination address and returns the previous one.
    pub fn set_dst(&mut self, addr: Ipv4Addr) -> Ipv4Addr {
        let old = core::mem::replace(&mut self.dst, addr.octets());
        self.checksum
            .set(checksum::update_bytes(self.checksum.get(), &old, &self.dst));
        Ipv4Addr::from(old)
    }
}

/// Folded sum of the header and its options.
///
/// Each part's sum is non-zero unless its bytes are, and one end-around
/// carry folds the pair, so this equals the sum of the contiguous bytes.
fn header_sum(header: &Ipv4Header, options: &[u8]) -> u16 {
    let acc = u32::from(checksum::sum(header.as_bytes())) + u32::from(checksum::sum(options));
    ((acc >> 16) + (acc & 0xffff)) as u16
}

fn header_checksum_is_valid(header: &Ipv4Header, options: &[u8]) -> bool {
    header_sum(header, options) == 0xffff
}

fn split_lengths(header: &Ipv4Header, available: usize) -> Result<(usize, usize), ParseError> {
    if header.version() != 4 {
        return Err(ParseError::Version { found: header.version() });
    }
    if header.ihl() < 5 {
        return Err(ParseError::HeaderLen { words: header.ihl() });
    }
    let header_len = header.header_len();
    if header_len > available {
        return Err(ParseError::Truncated { needed: header_len, available });
    }
    let total = usize::from(header.total_len());
    if total < header_len {
        return Err(ParseError::LengthBelowHeader { length: total, header_len });
    }
    if total > available {
        return Err(ParseError::LengthBeyondInput { length: total, available });
    }
    Ok((header_len - Ipv4Header::MIN_LEN, total - header_len))
}

/// Read-only IPv4 packet split into header, options and payload.
#[derive(Debug, Clone, Copy)]
#[non_exhaustive]
pub struct Ipv4Packet<'a> {
    pub header: &'a Ipv4Header,
    pub options: &'a [u8],
    /// Bytes after the header up to the total length.
    pub payload: &'a [u8],
}

impl<'a> Ipv4Packet<'a> {
    /// Splits an IPv4 packet at the start of the input.
    ///
    /// Checks the fixed header, the version, the header length, the options
    /// and the total length, in this order; the first failure is reported.
    ///
    /// # Errors
    ///
    /// - [`ParseError::Truncated`] when the input ends before the fixed header,
    ///   or before the options the header length counts.
    /// - [`ParseError::Version`] when the version is not 4.
    /// - [`ParseError::HeaderLen`] when the header length is below five words.
    /// - [`ParseError::LengthBelowHeader`] when the total length is below the
    ///   header length.
    /// - [`ParseError::LengthBeyondInput`] when the total length is beyond the
    ///   input.
    pub fn parse(buf: &'a [u8]) -> Result<Self, ParseError> {
        let available = buf.len();
        let (header, rest) = split_header::<Ipv4Header>(buf)?;
        let (options_len, payload_len) = split_lengths(header, available)?;
        // The lengths are bounded by the input, so neither split can panic.
        let (options, rest) = rest.split_at(options_len);
        Ok(Self {
            header,
            options,
            payload: &rest[..payload_len],
        })
    }

    /// Verifies the header checksum over header and options.
    pub fn checksum_is_valid(&self) -> bool {
        header_checksum_is_valid(self.header, self.options)
    }
}

/// Mutable IPv4 packet split into header, options and payload.
#[derive(Debug)]
#[non_exhaustive]
pub struct Ipv4PacketMut<'a> {
    pub header: &'a mut Ipv4Header,
    pub options: &'a mut [u8],
    /// Bytes after the header up to the total length.
    pub payload: &'a mut [u8],
}

impl<'a> Ipv4PacketMut<'a> {
    /// Same as [`Ipv4Packet::parse`] for in-place editing.
    ///
    /// # Errors
    ///
    /// The same as [`Ipv4Packet::parse`].
    pub fn parse(buf: &'a mut [u8]) -> Result<Self, ParseError> {
        let available = buf.len();
        let (header, rest) = split_header_mut::<Ipv4Header>(buf)?;
        let (options_len, payload_len) = split_lengths(header, available)?;
        let (options, rest) = rest.split_at_mut(options_len);
        Ok(Self {
            header,
            options,
            payload: &mut rest[..payload_len],
        })
    }

    /// Verifies the header checksum over header and options.
    pub fn checksum_is_valid(&self) -> bool {
        header_checksum_is_valid(self.header, self.options)
    }

    /// Recomputes the header checksum from scratch over header and options.
    pub fn recompute_checksum(&mut self) {
        self.header.checksum.set(0);
        let sum = header_sum(self.header, self.options);
        self.header.checksum.set(!sum);
    }
}

#[cfg(test)]
mod tests {

    use proptest::{collection::vec, prelude::*};

    use super::*;
    use crate::testing;

    /// Builds a valid packet with the given number of option words and payload.
    ///
    /// Returns the buffer and the packet length.
    fn build(opts: usize, payload: &[u8]) -> ([u8; 128], usize) {
        let mut buf = [0u8; 128];
        let hlen = 20 + opts * 4;
        let total = hlen + payload.len();
        buf[0] = 0x40 | (5 + opts) as u8;
        buf[1] = 0xb9;
        buf[2..4].copy_from_slice(&(total as u16).to_be_bytes());
        buf[4..6].copy_from_slice(&0x1c46u16.to_be_bytes());
        buf[6..8].copy_from_slice(&0x4000u16.to_be_bytes());
        buf[8] = 64;
        buf[9] = 6;
        buf[12..16].copy_from_slice(&[10, 0, 0, 1]);
        buf[16..20].copy_from_slice(&[192, 168, 0, 199]);
        for (i, b) in buf[20..hlen].iter_mut().enumerate() {
            *b = (i as u8).wrapping_mul(37).wrapping_add(1);
        }
        buf[hlen..total].copy_from_slice(payload);
        let sum = !checksum::sum(&buf[..hlen]);
        buf[10..12].copy_from_slice(&sum.to_be_bytes());
        (buf, total)
    }

    #[test]
    fn test_ipv4_parse_fields() {
        let (buf, total) = build(0, &[1, 2, 3]);
        let p = Ipv4Packet::parse(&buf[..total]).unwrap();
        let h = p.header;
        assert_eq!(4, h.version());
        assert_eq!(5, h.ihl());
        assert_eq!(20, h.header_len());
        assert_eq!(0x2e, h.dscp());
        assert_eq!(1, h.ecn());
        assert_eq!(23, h.total_len());
        assert_eq!(0x1c46, h.id());
        assert_eq!(0x4000, h.frag());
        assert!(!h.is_fragment());
        assert_eq!(64, h.ttl());
        assert_eq!(6, h.protocol());
        assert_eq!(Ipv4Addr::new(10, 0, 0, 1), h.src());
        assert_eq!(Ipv4Addr::new(192, 168, 0, 199), h.dst());
        assert!(p.options.is_empty());
        assert_eq!([1, 2, 3], p.payload);
        assert!(p.checksum_is_valid());
    }

    #[test]
    fn test_ipv4_parse_with_options() {
        let (buf, total) = build(3, &[9, 9]);
        let p = Ipv4Packet::parse(&buf[..total]).unwrap();
        assert_eq!(8, p.header.ihl());
        assert_eq!(32, p.header.header_len());
        assert_eq!(12, p.options.len());
        assert_eq!([9, 9], p.payload);
        assert!(p.checksum_is_valid());
    }

    #[test]
    fn test_ipv4_refuses_short_buffer() {
        let (mut buf, _) = build(0, &[1, 2, 3]);
        for len in 0..Ipv4Header::MIN_LEN {
            let expected = ParseError::Truncated { needed: 20, available: len };
            assert_eq!(expected, Ipv4Packet::parse(&buf[..len]).unwrap_err(), "len {len}");
        }
        let expected = ParseError::Truncated { needed: 20, available: 19 };
        assert_eq!(expected, Ipv4PacketMut::parse(&mut buf[..19]).unwrap_err());
    }

    #[test]
    fn test_ipv4_refuses_options_beyond_input() {
        let (mut buf, total) = build(4, &[]);
        for len in Ipv4Header::MIN_LEN..total {
            let expected = ParseError::Truncated { needed: 36, available: len };
            assert_eq!(expected, Ipv4Packet::parse(&buf[..len]).unwrap_err(), "len {len}");
        }
        let expected = ParseError::Truncated { needed: 36, available: 35 };
        assert_eq!(expected, Ipv4PacketMut::parse(&mut buf[..35]).unwrap_err());
        assert!(Ipv4Packet::parse(&buf[..total]).is_ok());
    }

    #[test]
    fn test_ipv4_refuses_malformed_header_length() {
        let (mut buf, total) = build(0, &[1, 2, 3, 4]);
        for ihl in 0..5u8 {
            buf[0] = 0x40 | ihl;
            let expected = ParseError::HeaderLen { words: ihl };
            assert_eq!(expected, Ipv4Packet::parse(&buf[..total]).unwrap_err(), "ihl {ihl}");
            assert_eq!(expected, Ipv4PacketMut::parse(&mut buf[..total]).unwrap_err());
        }
    }

    #[test]
    fn test_ipv4_refuses_wrong_version() {
        let (mut buf, total) = build(0, &[1, 2, 3, 4]);
        buf[0] = 0x65;
        let expected = ParseError::Version { found: 6 };
        assert_eq!(expected, Ipv4Packet::parse(&buf[..total]).unwrap_err());
        assert_eq!(expected, Ipv4PacketMut::parse(&mut buf[..total]).unwrap_err());
        // The version is checked before the header length.
        buf[0] = 0x60;
        assert_eq!(
            ParseError::Version { found: 6 },
            Ipv4Packet::parse(&buf[..total]).unwrap_err()
        );
    }

    #[test]
    fn test_ipv4_refuses_total_len_below_header() {
        let (mut buf, total) = build(0, &[1, 2, 3, 4]);
        buf[3] = 19;
        let expected = ParseError::LengthBelowHeader { length: 19, header_len: 20 };
        assert_eq!(expected, Ipv4Packet::parse(&buf[..total]).unwrap_err());
        assert_eq!(expected, Ipv4PacketMut::parse(&mut buf[..total]).unwrap_err());
    }

    #[test]
    fn test_ipv4_refuses_total_len_below_header_with_options() {
        let (mut buf, total) = build(2, &[1, 2, 3, 4]);
        buf[3] = 27;
        let expected = ParseError::LengthBelowHeader { length: 27, header_len: 28 };
        assert_eq!(expected, Ipv4Packet::parse(&buf[..total]).unwrap_err());
    }

    #[test]
    fn test_ipv4_header_len_beyond_total_len_refuses_length_below_header() {
        let (mut buf, _) = build(0, &[1, 2, 3, 4]);
        buf[0] = 0x4f;
        let expected = ParseError::LengthBelowHeader { length: 24, header_len: 60 };
        assert_eq!(expected, Ipv4Packet::parse(&buf[..64]).unwrap_err());
    }

    #[test]
    fn test_ipv4_header_len_beyond_input_reports_truncated_before_length() {
        let (mut buf, _) = build(0, &[]);
        buf[0] = 0x4f;
        buf[3] = 24;
        let expected = ParseError::Truncated { needed: 60, available: 30 };
        assert_eq!(expected, Ipv4Packet::parse(&buf[..30]).unwrap_err());
    }

    #[test]
    fn test_ipv4_refuses_total_len_beyond_input() {
        let (mut buf, total) = build(0, &[1, 2, 3, 4]);
        buf[3] = (total + 1) as u8;
        let expected = ParseError::LengthBeyondInput { length: total + 1, available: total };
        assert_eq!(expected, Ipv4Packet::parse(&buf[..total]).unwrap_err());
        assert_eq!(expected, Ipv4PacketMut::parse(&mut buf[..total]).unwrap_err());
    }

    #[test]
    fn test_ipv4_trailing_bytes_are_not_payload() {
        let (buf, total) = build(0, &[1, 2, 3]);
        let p = Ipv4Packet::parse(&buf[..total + 5]).unwrap();
        assert_eq!(3, p.payload.len());
    }

    #[test]
    fn test_ipv4_decrement_ttl_stops_at_one() {
        let (mut buf, total) = build(0, &[]);
        let p = Ipv4PacketMut::parse(&mut buf[..total]).unwrap();
        p.header.set_ttl(1);
        assert_eq!(None, p.header.decrement_ttl());
        assert_eq!(1, p.header.ttl());
        assert!(p.checksum_is_valid());
        p.header.set_ttl(0);
        assert_eq!(None, p.header.decrement_ttl());
    }

    /// Asserts that the incrementally maintained checksum is valid and equals
    /// a from-scratch recompute, up to the sign of zero.
    fn assert_matches_recompute(buf: &mut [u8]) {
        let mut p = Ipv4PacketMut::parse(buf).unwrap();
        assert!(p.checksum_is_valid());
        let incremental = p.header.checksum();
        p.recompute_checksum();
        let full = p.header.checksum();
        let zeros = [0, 0xffff];
        assert!(incremental == full || (zeros.contains(&incremental) && zeros.contains(&full)));
    }

    #[test]
    fn test_ipv4_decrement_ttl_matches_recompute_for_all_ttls() {
        for ttl in 2..=255u8 {
            let (mut buf, total) = build(0, &[]);
            let p = Ipv4PacketMut::parse(&mut buf[..total]).unwrap();
            p.header.set_ttl(ttl);
            assert_eq!(Some(ttl - 1), p.header.decrement_ttl());
            assert_matches_recompute(&mut buf[..total]);
        }
    }

    #[test]
    fn test_ipv4_setters_return_previous_address() {
        let (mut buf, total) = build(0, &[]);
        let p = Ipv4PacketMut::parse(&mut buf[..total]).unwrap();
        assert_eq!(Ipv4Addr::new(10, 0, 0, 1), p.header.set_src(Ipv4Addr::new(1, 1, 1, 1)));
        assert_eq!(
            Ipv4Addr::new(192, 168, 0, 199),
            p.header.set_dst(Ipv4Addr::new(2, 2, 2, 2))
        );
        assert_eq!(Ipv4Addr::new(1, 1, 1, 1), p.header.src());
    }

    #[test]
    fn test_ipv4_checksum_detects_corruption() {
        let (mut buf, total) = build(1, &[]);
        buf[22] ^= 1;
        assert!(!Ipv4Packet::parse(&buf[..total]).unwrap().checksum_is_valid());
    }

    /// IPv4 header fields as built and read back; the version is always 4 and
    /// the header length follows the options.
    #[derive(Debug, Clone, PartialEq, Eq)]
    struct Fields {
        dscp: u8,
        ecn: u8,
        total_len: u16,
        id: u16,
        frag: u16,
        ttl: u8,
        protocol: u8,
        checksum: u16,
        src: [u8; 4],
        dst: [u8; 4],
        options: Vec<u8>,
    }

    impl Fields {
        fn header_len(&self) -> usize {
            Ipv4Header::MIN_LEN + self.options.len()
        }

        fn to_bytes(&self, payload: &[u8]) -> Vec<u8> {
            let version_ihl = 0x40 | (5 + self.options.len() / 4) as u8;
            [
                &[version_ihl, self.dscp << 2 | self.ecn][..],
                &self.total_len.to_be_bytes(),
                &self.id.to_be_bytes(),
                &self.frag.to_be_bytes(),
                &[self.ttl, self.protocol],
                &self.checksum.to_be_bytes(),
                &self.src,
                &self.dst,
                &self.options,
                payload,
            ]
            .concat()
        }

        fn read(packet: &Ipv4Packet) -> Self {
            let header = packet.header;
            Self {
                dscp: header.dscp(),
                ecn: header.ecn(),
                total_len: header.total_len(),
                id: header.id(),
                frag: header.frag(),
                ttl: header.ttl(),
                protocol: header.protocol(),
                checksum: header.checksum(),
                src: header.src().octets(),
                dst: header.dst().octets(),
                options: packet.options.to_vec(),
            }
        }
    }

    /// A header with random fields and up to ten option words.
    ///
    /// The total length is a placeholder until a payload is known.
    fn fields() -> impl Strategy<Value = Fields> {
        (
            (0u8..64, 0u8..4),
            any::<(u16, u16, u16)>(),
            any::<(u8, u8)>(),
            any::<u16>(),
            any::<([u8; 4], [u8; 4])>(),
            vec(any::<[u8; 4]>(), 0..=10),
        )
            .prop_map(
                |((dscp, ecn), (total_len, id, frag), (ttl, protocol), checksum, (src, dst), options)| Fields {
                    dscp,
                    ecn,
                    total_len,
                    id,
                    frag,
                    ttl,
                    protocol,
                    checksum,
                    src,
                    dst,
                    options: options.concat(),
                },
            )
    }

    /// Fields whose total length covers the header and the returned payload.
    fn packet_fields() -> impl Strategy<Value = (Fields, Vec<u8>)> {
        (fields(), vec(any::<u8>(), 0..=48)).prop_map(|(mut fields, payload)| {
            fields.total_len = (fields.header_len() + payload.len()) as u16;
            (fields, payload)
        })
    }

    /// Offsets the property inputs may lie at: the version and header length
    /// byte, and both total length bytes.
    const LIES: &[usize] = &[0, 2, 3];

    /// A well-formed packet with a random checksum field.
    fn packet() -> impl Strategy<Value = Vec<u8>> {
        packet_fields().prop_map(|(fields, payload)| fields.to_bytes(&payload))
    }

    /// The header and payload parts of a parse, in input order, or its error.
    fn parts(buf: &[u8]) -> Result<Vec<&[u8]>, ParseError> {
        let packet = Ipv4Packet::parse(buf)?;
        Ok(vec![packet.header.as_bytes(), packet.options, packet.payload])
    }

    /// The parts as above, through the mutable parse.
    fn parts_mut(buf: &mut [u8]) -> Result<Vec<&[u8]>, ParseError> {
        let Ipv4PacketMut { header, options, payload } = Ipv4PacketMut::parse(buf)?;
        Ok(vec![header.as_bytes(), &*options, &*payload])
    }

    /// A mutation of the IPv4 header through its setters.
    #[derive(Debug, Clone)]
    enum Op {
        Dscp(u8),
        Ecn(u8),
        Id(u16),
        Ttl(u8),
        DecrementTtl,
        Protocol(u8),
        /// Taken modulo the room the buffer leaves for the payload.
        TotalLen(u16),
        Src([u8; 4]),
        Dst([u8; 4]),
    }

    /// Random sequences of header mutations.
    fn ops() -> impl Strategy<Value = Vec<Op>> {
        let op = prop_oneof![
            any::<u8>().prop_map(Op::Dscp),
            any::<u8>().prop_map(Op::Ecn),
            any::<u16>().prop_map(Op::Id),
            any::<u8>().prop_map(Op::Ttl),
            Just(Op::DecrementTtl),
            any::<u8>().prop_map(Op::Protocol),
            any::<u16>().prop_map(Op::TotalLen),
            any::<[u8; 4]>().prop_map(Op::Src),
            any::<[u8; 4]>().prop_map(Op::Dst),
        ];
        vec(op, 0..=16)
    }

    /// Runs the mutations on a packet with a valid checksum.
    ///
    /// Returns the buffer and the model of the fields after the same
    /// mutations, with the checksum as stored.
    fn run(start: &Fields, payload: &[u8], ops: &[Op]) -> (Vec<u8>, Fields) {
        let mut buf = start.to_bytes(payload);
        Ipv4PacketMut::parse(&mut buf).unwrap().recompute_checksum();
        let mut model = Ipv4Packet::parse(&buf).map(|packet| Fields::read(&packet)).unwrap();
        for op in ops {
            let header = Ipv4PacketMut::parse(&mut buf).unwrap().header;
            match *op {
                Op::Dscp(dscp) => {
                    header.set_dscp(dscp);
                    model.dscp = dscp & 0x3f;
                }
                Op::Ecn(ecn) => {
                    header.set_ecn(ecn);
                    model.ecn = ecn & 3;
                }
                Op::Id(id) => {
                    header.set_id(id);
                    model.id = id;
                }
                Op::Ttl(ttl) => {
                    header.set_ttl(ttl);
                    model.ttl = ttl;
                }
                Op::DecrementTtl => {
                    let expected = (model.ttl > 1).then(|| model.ttl - 1);
                    assert_eq!(expected, header.decrement_ttl());
                    model.ttl = expected.unwrap_or(model.ttl);
                }
                Op::Protocol(protocol) => {
                    header.set_protocol(protocol);
                    model.protocol = protocol;
                }
                Op::TotalLen(total_len) => {
                    let room = payload.len() + 1;
                    let total_len = (start.header_len() + usize::from(total_len) % room) as u16;
                    header.set_total_len(total_len);
                    model.total_len = total_len;
                }
                Op::Src(src) => {
                    assert_eq!(Ipv4Addr::from(model.src), header.set_src(Ipv4Addr::from(src)));
                    model.src = src;
                }
                Op::Dst(dst) => {
                    assert_eq!(Ipv4Addr::from(model.dst), header.set_dst(Ipv4Addr::from(dst)));
                    model.dst = dst;
                }
            }
            model.checksum = header.checksum();
        }
        (buf, model)
    }

    /// Asserts that the error carries the values the input bytes hold.
    fn assert_error(buf: &[u8], error: ParseError) {
        let version = buf.first().map(|byte| byte >> 4);
        let words = buf.first().map(|byte| byte & 0x0f);
        match error {
            ParseError::Truncated { needed, available } => {
                assert_eq!(buf.len(), available);
                assert!(needed > available);
                if buf.len() < Ipv4Header::MIN_LEN {
                    assert_eq!(Ipv4Header::MIN_LEN, needed);
                } else {
                    assert_eq!(Some(4), version);
                    assert_eq!(usize::from(words.unwrap()) * 4, needed);
                }
            }
            ParseError::Version { found } => {
                assert_eq!(version, Some(found));
                assert_ne!(4, found);
            }
            ParseError::HeaderLen { words: found } => {
                assert_eq!(Some(4), version);
                assert_eq!(words, Some(found));
                assert!(found < 5);
            }
            ParseError::LengthBelowHeader { length, header_len } => {
                assert_eq!(testing::be16(&buf[2..]), length);
                assert_eq!(usize::from(words.unwrap()) * 4, header_len);
                assert!(length < header_len && header_len <= buf.len());
            }
            ParseError::LengthBeyondInput { length, available } => {
                assert_eq!(testing::be16(&buf[2..]), length);
                assert_eq!(buf.len(), available);
                assert!(length > available);
            }
        }
    }

    proptest! {
        #![proptest_config(testing::config())]

        #[test]
        fn test_ipv4_parse_ok_respects_bounds(buf in testing::input(packet(), LIES)) {
            if let Ok(parts) = parts(&buf) {
                testing::assert_adjacent(&buf, &parts);
                let header = Ipv4Header::read_from_bytes(parts[0]).unwrap();
                prop_assert_eq!(Ipv4Header::MIN_LEN, parts[0].len());
                prop_assert_eq!(header.header_len(), parts[0].len() + parts[1].len());
                prop_assert_eq!(usize::from(header.total_len()), header.header_len() + parts[2].len());
                prop_assert!(usize::from(header.total_len()) <= buf.len());
            }
        }

        #[test]
        fn test_ipv4_parse_error_reports_fields_of_input(buf in testing::input(packet(), LIES)) {
            if let Err(error) = parts(&buf) {
                assert_error(&buf, error);
            }
        }

        #[test]
        fn test_ipv4_parse_and_parse_mut_agree(buf in testing::input(packet(), LIES)) {
            let mut copy = buf.clone();
            prop_assert_eq!(testing::lens(&parts(&buf)), testing::lens(&parts_mut(&mut copy)));
        }

        #[test]
        fn test_ipv4_round_trip_reads_back_built_fields((fields, payload) in packet_fields()) {
            let buf = fields.to_bytes(&payload);
            let packet = Ipv4Packet::parse(&buf).unwrap();
            prop_assert_eq!(4, packet.header.version());
            prop_assert_eq!(&fields, &Fields::read(&packet));
            prop_assert_eq!(&payload[..], packet.payload);
        }

        #[test]
        fn test_ipv4_setters_change_only_their_field((start, payload) in packet_fields(), ops in ops()) {
            let (buf, model) = run(&start, &payload, &ops);
            let packet = Ipv4Packet::parse(&buf).unwrap();
            prop_assert_eq!(&model, &Fields::read(&packet));
            let payload_len = usize::from(model.total_len) - start.header_len();
            prop_assert_eq!(&payload[..payload_len], packet.payload);
        }

        #[test]
        fn test_ipv4_incremental_checksum_equals_recompute((start, payload) in packet_fields(), ops in ops()) {
            let (mut buf, _) = run(&start, &payload, &ops);
            let mut packet = Ipv4PacketMut::parse(&mut buf).unwrap();
            prop_assert!(packet.checksum_is_valid());
            let incremental = packet.header.checksum();
            packet.recompute_checksum();
            prop_assert!(testing::same_checksum(incremental, packet.header.checksum()));
        }
    }
}
