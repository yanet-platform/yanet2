use core::net::{Ipv4Addr, Ipv6Addr};

use zerocopy::{
    FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned,
    byteorder::network_endian::{U16, U32},
};

use crate::{
    Adjusted, IPPROTO_TCP, Mode, ParseError, Unadjusted, checksum,
    mutation::{self, Sealed},
    split_header, split_header_mut,
};

pub mod options;

/// The bits of the data offset and flags word that hold the nine flags.
const FLAG_BITS: u16 = 0x01ff;

/// TCP fixed header (20 bytes, options excluded).
///
/// The fields change through a mutation, which keeps the checksum valid and
/// never stores it as zero, or stores the fields alone for a checksum filled
/// over the pseudo header and the segment after it.
#[derive(Debug, Clone, Copy, PartialEq, Eq, FromBytes, IntoBytes, KnownLayout, Immutable, Unaligned)]
#[repr(C)]
pub struct TcpHeader {
    src_port: U16,
    dst_port: U16,
    seq: U32,
    ack: U32,
    /// The data offset, three reserved bits and the nonce flag.
    offset: u8,
    flags: u8,
    window: U16,
    checksum: U16,
    urgent: U16,
}

impl TcpHeader {
    /// Bytes of the header without options.
    pub const MIN_LEN: usize = 20;
    /// Flag bit: the sender has no more data.
    pub const FIN: u16 = 0x01;
    /// Flag bit: synchronise sequence numbers.
    pub const SYN: u16 = 0x02;
    /// Flag bit: reset the connection.
    pub const RST: u16 = 0x04;
    /// Flag bit: push the data to the receiving application.
    pub const PSH: u16 = 0x08;
    /// Flag bit: the acknowledgment number is significant.
    pub const ACK: u16 = 0x10;
    /// Flag bit: the urgent pointer is significant.
    pub const URG: u16 = 0x20;

    /// Source port, in host byte order.
    pub fn src_port(&self) -> u16 {
        self.src_port.get()
    }

    /// Destination port, in host byte order.
    pub fn dst_port(&self) -> u16 {
        self.dst_port.get()
    }

    /// Sequence number of the first data byte, or with SYN set the initial
    /// sequence number, in host byte order.
    pub fn seq(&self) -> u32 {
        self.seq.get()
    }

    /// Acknowledgment number, in host byte order; with ACK set, the next
    /// sequence number the sender expects.
    pub fn ack(&self) -> u32 {
        self.ack.get()
    }

    /// Header length in 32-bit words.
    pub fn data_offset(&self) -> u8 {
        self.offset >> 4
    }

    /// Header length in bytes, options included.
    pub fn header_len(&self) -> usize {
        usize::from(self.data_offset()) * 4
    }

    /// Flag bits, including the nonce bit.
    pub fn flags(&self) -> u16 {
        self.offset_flags() & FLAG_BITS
    }

    /// The data offset and flags word, in host byte order.
    fn offset_flags(&self) -> u16 {
        u16::from_be_bytes([self.offset, self.flags])
    }

    /// Receive window, before any window scaling.
    pub fn window(&self) -> u16 {
        self.window.get()
    }

    /// Checksum as stored.
    pub fn checksum(&self) -> u16 {
        self.checksum.get()
    }

    /// Urgent pointer, in host byte order; with URG set, the offset from the
    /// sequence number to the octet that follows the urgent data.
    pub fn urgent(&self) -> u16 {
        self.urgent.get()
    }

    /// Writes a header without options at the start of the buffer and
    /// starts an unadjusted mutation of it: the data offset five words, every
    /// other field zero.
    ///
    /// The bytes the header covers are not read, so it may land on a copy of
    /// another packet's headers or on a fresh buffer alike. Its checksum is
    /// filled once the segment is in place.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the buffer is shorter than the header.
    #[inline]
    pub fn init(buf: &mut [u8]) -> Result<TcpMutation<'_, Unadjusted>, ParseError> {
        let (header, _) = split_header_mut::<Self>(buf)?;
        *header = Self {
            src_port: U16::ZERO,
            dst_port: U16::ZERO,
            seq: U32::ZERO,
            ack: U32::ZERO,
            offset: ((Self::MIN_LEN / 4) as u8) << 4,
            flags: 0,
            window: U16::ZERO,
            checksum: U16::ZERO,
            urgent: U16::ZERO,
        };
        Ok(header.mutate_unadjusted())
    }

    /// The fixed header at the start of the input and the bytes after it,
    /// its fields unchecked.
    ///
    /// Reads the header of a segment the input does not hold whole, such as
    /// the first segment of a chained frame or a first fragment.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the input is shorter than the fixed
    /// header.
    #[inline]
    pub fn from_prefix(buf: &[u8]) -> Result<(&Self, &[u8]), ParseError> {
        split_header(buf)
    }

    /// Starts a mutation that keeps the checksum valid.
    #[inline]
    pub fn mutate(&mut self) -> TcpMutation<'_> {
        TcpMutation {
            header: self,
            options: &mut [],
            mode: mutation::adjusted(),
        }
    }

    /// Starts a mutation that stores the fields alone, for a checksum filled
    /// anew after it.
    #[inline]
    pub fn mutate_unadjusted(&mut self) -> TcpMutation<'_, Unadjusted> {
        TcpMutation {
            header: self,
            options: &mut [],
            mode: Unadjusted,
        }
    }

    /// Fills the checksum of the segment the bytes hold, its header, options
    /// and payload, over the IPv4 pseudo header.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the bytes are shorter than the fixed
    /// header.
    #[inline]
    pub fn fill_checksum_ipv4(segment: &mut [u8], src: Ipv4Addr, dst: Ipv4Addr) -> Result<(), ParseError> {
        split_header_mut::<Self>(segment)?.0.checksum = U16::ZERO;
        let sum = checksum::l4_checksum_ipv4(src, dst, IPPROTO_TCP, segment);
        split_header_mut::<Self>(segment)?.0.checksum.set(sum);
        Ok(())
    }

    /// Fills the checksum of the segment the bytes hold over the IPv6 pseudo
    /// header, as [`TcpHeader::fill_checksum_ipv4`] does over IPv4.
    ///
    /// # Errors
    ///
    /// The same as [`TcpHeader::fill_checksum_ipv4`].
    #[inline]
    pub fn fill_checksum_ipv6(segment: &mut [u8], src: Ipv6Addr, dst: Ipv6Addr) -> Result<(), ParseError> {
        split_header_mut::<Self>(segment)?.0.checksum = U16::ZERO;
        let sum = checksum::l4_checksum_ipv6(src, dst, IPPROTO_TCP, segment);
        split_header_mut::<Self>(segment)?.0.checksum.set(sum);
        Ok(())
    }
}

/// A change of TCP header fields, in the mode chosen when it starts.
///
/// An adjusting mutation ends with [`TcpMutation::finish`], adjusting the
/// checksum once for every change; an unadjusted one stores the fields alone
/// and ends with [`TcpMutation::finish_unadjusted`], the checksum filled
/// anew after it. One started from a [`TcpPacketMut`] also edits options in
/// place under the same adjustment. Setters chain, and a chain left without
/// its finish is an unused value the compiler warns about, though an early
/// return between the start and the finish goes unseen.
#[must_use = "a mutation ends with finish or finish_unadjusted"]
#[derive(Debug)]
pub struct TcpMutation<'a, M = Adjusted> {
    header: &'a mut TcpHeader,
    /// The options area, empty for a mutation of the fixed header alone.
    options: &'a mut [u8],
    mode: M,
}

impl<M> TcpMutation<'_, M>
where
    M: Mode,
{
    /// Sets the source port.
    #[inline]
    pub fn set_src_port(mut self, port: u16) -> Self {
        let old = mutation::word16(self.header.src_port);
        self.header.src_port.set(port);
        self.mode.add16(old, port.to_be());
        self
    }

    /// Sets the destination port.
    #[inline]
    pub fn set_dst_port(mut self, port: u16) -> Self {
        let old = mutation::word16(self.header.dst_port);
        self.header.dst_port.set(port);
        self.mode.add16(old, port.to_be());
        self
    }

    /// Sets the sequence number.
    #[inline]
    pub fn set_seq(mut self, seq: u32) -> Self {
        let old = mutation::word32(self.header.seq);
        self.header.seq.set(seq);
        self.mode.add32(old, seq.to_be());
        self
    }

    /// Sets the acknowledgment number.
    #[inline]
    pub fn set_ack(mut self, ack: u32) -> Self {
        let old = mutation::word32(self.header.ack);
        self.header.ack.set(ack);
        self.mode.add32(old, ack.to_be());
        self
    }

    /// Sets the flags; the data offset is kept and the bits above the nine
    /// flags are dropped.
    #[inline]
    pub fn set_flags(mut self, flags: u16) -> Self {
        let old = self.header.offset_flags();
        let word = (old & !FLAG_BITS) | (flags & FLAG_BITS);
        self.header.offset = (word >> 8) as u8;
        self.header.flags = word as u8;
        self.mode.add16(old.to_be(), word.to_be());
        self
    }

    /// Sets the window.
    #[inline]
    pub fn set_window(mut self, window: u16) -> Self {
        let old = mutation::word16(self.header.window);
        self.header.window.set(window);
        self.mode.add16(old, window.to_be());
        self
    }

    /// Sets the urgent pointer.
    #[inline]
    pub fn set_urgent(mut self, urgent: u16) -> Self {
        let old = mutation::word16(self.header.urgent);
        self.header.urgent.set(urgent);
        self.mode.add16(old, urgent.to_be());
        self
    }
}

impl<'a> TcpMutation<'a> {
    /// Accounts a change of the IPv4 source or destination, which the
    /// pseudo header carries.
    #[inline]
    pub fn replace_ipv4_addr(mut self, old: Ipv4Addr, new: Ipv4Addr) -> Self {
        self.mode.add_octets(old.octets(), new.octets());
        self
    }

    /// Accounts a change of the IPv6 source or destination, which the
    /// pseudo header carries.
    #[inline]
    pub fn replace_ipv6_addr(mut self, old: Ipv6Addr, new: Ipv6Addr) -> Self {
        self.mode.add_octets(old.octets(), new.octets());
        self
    }

    /// Ends the mutation: adjusts the checksum once for every change, never
    /// storing it as zero.
    #[inline]
    pub fn finish(self) -> &'a mut TcpHeader {
        let Self { header, mode, .. } = self;
        header.checksum = mutation::adjust_no_zero(header.checksum, mode);
        header
    }
}

impl<'a> TcpMutation<'a, Unadjusted> {
    /// Ends the mutation and leaves the checksum to be filled.
    #[inline]
    pub fn finish_unadjusted(self) -> &'a mut TcpHeader {
        self.header
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
    /// Options, the bytes the data offset counts past the fixed header.
    pub options: &'a [u8],
    /// Bytes after the options up to the end of the input.
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
    /// Options, the bytes the data offset counts past the fixed header.
    pub options: &'a mut [u8],
    /// Bytes after the options up to the end of the input.
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

    /// Starts a mutation of the header and its options that keeps the
    /// checksum valid.
    ///
    /// # Errors
    ///
    /// [`ParseError::OptionLength`] for an option whose length cannot be
    /// trusted, before anything changes.
    #[inline]
    pub fn mutate(&mut self) -> Result<TcpMutation<'_>, ParseError> {
        self.mutation(mutation::adjusted())
    }

    /// Starts a mutation of the header and its options that stores the
    /// changes alone, for a checksum filled anew after it.
    ///
    /// # Errors
    ///
    /// The same as [`TcpPacketMut::mutate`].
    #[inline]
    pub fn mutate_unadjusted(&mut self) -> Result<TcpMutation<'_, Unadjusted>, ParseError> {
        self.mutation(Unadjusted)
    }

    #[inline]
    fn mutation<M>(&mut self, mode: M) -> Result<TcpMutation<'_, M>, ParseError> {
        options::options(self.options).try_for_each(|option| option.map(drop))?;
        Ok(TcpMutation {
            header: self.header,
            options: self.options,
            mode,
        })
    }
}

#[cfg(test)]
mod tests {

    use proptest::{collection::vec, prelude::*};

    use super::*;
    use crate::testing::{self, Family, L4Op};

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

    #[test]
    fn test_tcp_from_prefix_reads_header_of_segment_beyond_input() {
        let (buf, _) = build([1, 2, 3, 4], [5, 6, 7, 8], 2, &[7, 7, 7]);
        let (header, rest) = TcpHeader::from_prefix(&buf[..20]).unwrap();
        assert_eq!(7, header.data_offset());
        assert_eq!(0x01020304, header.seq());
        assert!(rest.is_empty());
    }

    #[test]
    fn test_tcp_from_prefix_refuses_short_input() {
        let (buf, _) = build([1, 2, 3, 4], [5, 6, 7, 8], 0, &[]);
        for len in 0..TcpHeader::MIN_LEN {
            let expected = ParseError::Truncated { needed: 20, available: len };
            assert_eq!(expected, TcpHeader::from_prefix(&buf[..len]).unwrap_err(), "len {len}");
        }
    }

    #[test]
    fn test_tcp_init_refuses_short_buffer() {
        let mut buf = [0u8; 20];
        for len in 0..TcpHeader::MIN_LEN {
            let expected = ParseError::Truncated { needed: 20, available: len };
            assert_eq!(expected, TcpHeader::init(&mut buf[..len]).unwrap_err(), "len {len}");
        }
    }

    #[test]
    fn test_tcp_fill_checksum_refuses_short_segment() {
        let mut buf = [0u8; 20];
        let (src, dst) = (Ipv4Addr::new(10, 0, 0, 1), Ipv4Addr::new(10, 0, 0, 2));
        for len in 0..TcpHeader::MIN_LEN {
            let expected = ParseError::Truncated { needed: 20, available: len };
            let error = TcpHeader::fill_checksum_ipv4(&mut buf[..len], src, dst).unwrap_err();
            assert_eq!(expected, error, "len {len}");
        }
    }

    #[test]
    fn test_tcp_mutation_never_yields_zero_checksum() {
        let mut header = TcpHeader::read_from_bytes(&[0; 20]).unwrap();
        // Inner sum all-ones would store zero; it must be stored as all-ones.
        header.checksum.set(0x0001);
        let header = header
            .mutate()
            .replace_ipv4_addr(Ipv4Addr::new(0, 0, 0, 0), Ipv4Addr::new(0, 0, 0, 1))
            .finish();
        assert_eq!(0xffff, header.checksum());
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

        fn read(packet: &TcpPacket<'_>) -> Self {
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

    /// A change of a TCP header: one of those the L4 headers share, or a
    /// change of a field only TCP carries.
    #[derive(Debug, Clone)]
    enum TcpOp {
        L4(L4Op),
        Seq(u32),
        Ack(u32),
        Flags(u16),
        Window(u16),
        Urgent(u16),
    }

    /// Random mutations of a TCP header, the shared changes as likely as the
    /// TCP ones together.
    fn tcp_mutations() -> impl Strategy<Value = Vec<Vec<TcpOp>>> {
        testing::mutations(prop_oneof![
            5 => testing::l4_op().prop_map(TcpOp::L4),
            1 => any::<u32>().prop_map(TcpOp::Seq),
            1 => any::<u32>().prop_map(TcpOp::Ack),
            1 => any::<u16>().prop_map(TcpOp::Flags),
            1 => any::<u16>().prop_map(TcpOp::Window),
            1 => any::<u16>().prop_map(TcpOp::Urgent),
        ])
    }

    /// Applies the change through the mutation and tracks the pseudo-header
    /// source and destination.
    fn apply<'a>(
        mutation: TcpMutation<'a>,
        family: Family,
        op: &TcpOp,
        addrs: &mut ([u8; 16], [u8; 16]),
    ) -> TcpMutation<'a> {
        match *op {
            TcpOp::L4(ref op) => testing::apply(mutation, family, op, addrs),
            TcpOp::Seq(seq) => mutation.set_seq(seq),
            TcpOp::Ack(ack) => mutation.set_ack(ack),
            TcpOp::Flags(flags) => mutation.set_flags(flags),
            TcpOp::Window(window) => mutation.set_window(window),
            TcpOp::Urgent(urgent) => mutation.set_urgent(urgent),
        }
    }

    impl testing::L4Mutation for TcpMutation<'_> {
        fn set_src_port(self, port: u16) -> Self {
            TcpMutation::set_src_port(self, port)
        }

        fn set_dst_port(self, port: u16) -> Self {
            TcpMutation::set_dst_port(self, port)
        }

        fn replace_ipv4_addr(self, old: Ipv4Addr, new: Ipv4Addr) -> Self {
            TcpMutation::replace_ipv4_addr(self, old, new)
        }

        fn replace_ipv6_addr(self, old: Ipv6Addr, new: Ipv6Addr) -> Self {
            TcpMutation::replace_ipv6_addr(self, old, new)
        }
    }

    /// Fills the segment's checksum over the family's pseudo header of the
    /// addresses.
    fn fill(segment: &mut [u8], family: Family, (src, dst): ([u8; 16], [u8; 16])) -> Result<(), ParseError> {
        match family {
            Family::V4 => TcpHeader::fill_checksum_ipv4(segment, Family::v4(src), Family::v4(dst)),
            Family::V6 => TcpHeader::fill_checksum_ipv6(segment, Ipv6Addr::from(src), Ipv6Addr::from(dst)),
        }
    }

    /// The fields of a header without options.
    fn bare(fields: Fields) -> Fields {
        Fields { options: Vec::new(), ..fields }
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
            ParseError::OptionLength { .. } | ParseError::LengthBeyondField { .. } => panic!("unexpected {error:?}"),
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
        fn test_tcp_mutation_changes_only_its_fields(
            start in fields(),
            payload in vec(any::<u8>(), 0..=32),
            family in testing::family(),
            addrs in any::<([u8; 16], [u8; 16])>(),
            mutations in tcp_mutations(),
        ) {
            let mut buf = start.to_bytes(&payload);
            let mut model = start.clone();
            let mut addrs = addrs;
            for ops in &mutations {
                let mut mutation = TcpPacketMut::parse(&mut buf).unwrap().header.mutate();
                for op in ops {
                    mutation = apply(mutation, family, op, &mut addrs);
                    match *op {
                        TcpOp::L4(L4Op::SrcPort(port)) => model.src_port = port,
                        TcpOp::L4(L4Op::DstPort(port)) => model.dst_port = port,
                        TcpOp::L4(L4Op::Src(_) | L4Op::Dst(_)) => {}
                        TcpOp::Seq(seq) => model.seq = seq,
                        TcpOp::Ack(ack) => model.ack = ack,
                        TcpOp::Flags(flags) => model.flags = flags & 0x01ff,
                        TcpOp::Window(window) => model.window = window,
                        TcpOp::Urgent(urgent) => model.urgent = urgent,
                    }
                }
                model.checksum = mutation.finish().checksum();
            }
            let packet = TcpPacket::parse(&buf).unwrap();
            prop_assert_eq!(&model, &Fields::read(&packet));
            prop_assert_eq!(&payload[..], packet.payload);
        }

        #[test]
        fn test_tcp_mutation_checksum_verifies_over_pseudo_header(
            start in fields(),
            payload in vec(any::<u8>(), 0..=32),
            family in testing::family(),
            addrs in any::<([u8; 16], [u8; 16])>(),
            mutations in tcp_mutations(),
        ) {
            let start = Fields { checksum: 0, ..start };
            let mut buf = start.to_bytes(&payload);
            let sum = family.l4_checksum(addrs.0, addrs.1, IPPROTO_TCP, &buf);
            buf[16..18].copy_from_slice(&sum.to_be_bytes());
            let mut addrs = addrs;
            for ops in &mutations {
                let header = TcpPacketMut::parse(&mut buf).unwrap().header;
                let mutation = ops
                    .iter()
                    .fold(header.mutate(), |mutation, op| apply(mutation, family, op, &mut addrs));
                prop_assert_ne!(0, mutation.finish().checksum());
            }
            prop_assert!(family.l4_is_valid(addrs.0, addrs.1, IPPROTO_TCP, &buf));
        }

        #[test]
        fn test_tcp_unadjusted_mutation_stores_fields_alone(
            start in fields(),
            new in fields(),
            payload in vec(any::<u8>(), 0..=32),
        ) {
            let mut buf = start.to_bytes(&payload);
            TcpPacketMut::parse(&mut buf)
                .unwrap()
                .header
                .mutate_unadjusted()
                .set_src_port(new.src_port)
                .set_dst_port(new.dst_port)
                .set_seq(new.seq)
                .set_ack(new.ack)
                .set_flags(new.flags)
                .set_window(new.window)
                .set_urgent(new.urgent)
                .finish_unadjusted();
            let packet = TcpPacket::parse(&buf).unwrap();
            let expected = Fields { checksum: start.checksum, options: start.options.clone(), ..new };
            prop_assert_eq!(expected, Fields::read(&packet));
            prop_assert_eq!(&payload[..], packet.payload);
        }

        #[test]
        fn test_tcp_init_writes_empty_header_over_junk(junk in any::<[u8; 20]>()) {
            let mut buf = junk;
            TcpHeader::init(&mut buf).unwrap().finish_unadjusted();
            let packet = TcpPacket::parse(&buf).unwrap();
            let empty = Fields {
                src_port: 0,
                dst_port: 0,
                seq: 0,
                ack: 0,
                flags: 0,
                window: 0,
                checksum: 0,
                urgent: 0,
                options: Vec::new(),
            };
            prop_assert_eq!(empty, Fields::read(&packet));
        }

        #[test]
        fn test_tcp_init_and_setters_read_back_fields(fields in fields(), junk in any::<[u8; 20]>()) {
            let mut buf = junk;
            TcpHeader::init(&mut buf)
                .unwrap()
                .set_src_port(fields.src_port)
                .set_dst_port(fields.dst_port)
                .set_seq(fields.seq)
                .set_ack(fields.ack)
                .set_flags(fields.flags)
                .set_window(fields.window)
                .set_urgent(fields.urgent)
                .finish_unadjusted();
            let packet = TcpPacket::parse(&buf).unwrap();
            prop_assert_eq!(bare(Fields { checksum: 0, ..fields }), Fields::read(&packet));
        }

        #[test]
        fn test_tcp_fill_checksum_verifies_over_pseudo_header(
            start in fields(),
            payload in vec(any::<u8>(), 0..=32),
            family in testing::family(),
            addrs in any::<([u8; 16], [u8; 16])>(),
        ) {
            let mut buf = start.to_bytes(&payload);
            fill(&mut buf, family, addrs).unwrap();
            prop_assert!(family.l4_is_valid(addrs.0, addrs.1, IPPROTO_TCP, &buf));
        }
    }
}
