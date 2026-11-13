use core::net::Ipv4Addr;

use zerocopy::{FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned, byteorder::network_endian::U16};

use crate::{Adjusted, Mode, ParseError, Unadjusted, checksum, mutation, split_header, split_header_mut};

/// IPv4 fixed header (20 bytes, options excluded).
///
/// The header checksum covers every field, so the fields change through a
/// mutation: one that keeps the checksum valid, or one that stores the fields
/// alone for a checksum filled after it. A rewritten address must also be
/// reported to the L4 header to fix its pseudo-header checksum.
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
    /// Bytes of the header without options.
    pub const MIN_LEN: usize = 20;

    /// Writes a header without options at the start of the buffer and
    /// starts an unadjusted mutation of it: version 4 and five words long,
    /// every other field zero.
    ///
    /// The bytes the header covers are not read, so it may land on a copy of
    /// another packet's headers or on a fresh buffer alike. Its checksum is
    /// filled once the fields are set.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the buffer is shorter than the header.
    #[inline]
    pub fn init(buf: &mut [u8]) -> Result<Ipv4Mutation<'_, Unadjusted>, ParseError> {
        let (header, _) = split_header_mut::<Self>(buf)?;
        // Two stores: the fields after the first eight bytes are those a
        // header built anew sets, so their own stores replace the second.
        let (head, tail) = header.as_mut_bytes().split_at_mut(8);
        head.copy_from_slice(&[(4 << 4) | (Self::MIN_LEN / 4) as u8, 0, 0, 0, 0, 0, 0, 0]);
        tail.fill(0);
        Ok(header.mutate_unadjusted())
    }

    /// The fixed header at the start of the input and the bytes after it,
    /// its fields unchecked.
    ///
    /// Reads the header of a packet the input does not hold whole, such as a
    /// copy of its headers or the first segment of a chained frame.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the input is shorter than the fixed
    /// header.
    #[inline]
    pub fn from_prefix(buf: &[u8]) -> Result<(&Self, &[u8]), ParseError> {
        split_header(buf)
    }

    /// Same as [`Ipv4Header::from_prefix`] for in-place editing.
    ///
    /// # Errors
    ///
    /// The same as [`Ipv4Header::from_prefix`].
    #[inline]
    pub fn from_prefix_mut(buf: &mut [u8]) -> Result<(&mut Self, &mut [u8]), ParseError> {
        split_header_mut(buf)
    }

    /// Starts a mutation that keeps the checksum valid.
    #[inline]
    pub fn mutate(&mut self) -> Ipv4Mutation<'_> {
        Ipv4Mutation {
            header: self,
            mode: mutation::adjusted(),
        }
    }

    /// Starts a mutation that stores the fields alone, for a checksum filled
    /// anew after it.
    #[inline]
    pub fn mutate_unadjusted(&mut self) -> Ipv4Mutation<'_, Unadjusted> {
        Ipv4Mutation { header: self, mode: Unadjusted }
    }

    /// Fills the checksum of a header without options, over its fixed part.
    ///
    /// A header with options takes its checksum over them too, from the
    /// packet's view of both.
    #[inline]
    pub fn fill_checksum(&mut self) {
        fill_header_checksum(self, &[]);
    }

    /// IP version, 4 for a header that parsed.
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

    /// Differentiated services codepoint, 0 to 63.
    pub fn dscp(&self) -> u8 {
        self.tos >> 2
    }

    /// Explicit congestion notification codepoint, 0 to 3.
    pub fn ecn(&self) -> u8 {
        self.tos & 3
    }

    /// Length of the packet in bytes, header included.
    pub fn total_len(&self) -> u16 {
        self.total_len.get()
    }

    /// Identification, shared by the fragments of a packet.
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

    /// Time to live: the hops left before a router discards the packet.
    pub fn ttl(&self) -> u8 {
        self.ttl
    }

    /// IP protocol number of the payload.
    pub fn protocol(&self) -> u8 {
        self.protocol
    }

    /// Header checksum as stored.
    pub fn checksum(&self) -> u16 {
        self.checksum.get()
    }

    /// Source address as the packet carries it now; after NAT or
    /// encapsulation it may differ from the original sender's.
    pub fn src(&self) -> Ipv4Addr {
        Ipv4Addr::from(self.src)
    }

    /// Destination address as the packet carries it now; after NAT or
    /// encapsulation it may differ from the original receiver's.
    pub fn dst(&self) -> Ipv4Addr {
        Ipv4Addr::from(self.dst)
    }

    /// Decrements the TTL and returns the new value.
    ///
    /// Returns `None` and leaves the header untouched when the TTL is already
    /// 1 or 0, i.e. the packet must not be forwarded.
    #[must_use = "a packet whose TTL cannot be decremented must not be forwarded"]
    #[inline]
    pub fn decrement_ttl(&mut self) -> Option<u8> {
        if self.ttl <= 1 {
            return None;
        }
        let ttl = self.ttl - 1;
        self.mutate().set_ttl(ttl).finish();
        Some(ttl)
    }
}

/// A change of IPv4 header fields, in the mode chosen when it starts.
///
/// An adjusting mutation keeps the header checksum valid and ends with
/// [`Ipv4Mutation::finish`], which adjusts it once for every change; an
/// unadjusted one stores the fields alone and ends with
/// [`Ipv4Mutation::finish_unadjusted`], the checksum then filled anew. Each
/// setter takes the mutation and returns it, so the changes chain; a chain
/// left without its finish is an unused value the compiler warns about,
/// though an early return between the start and the finish goes unseen.
#[must_use = "a mutation ends with finish or finish_unadjusted"]
#[derive(Debug)]
pub struct Ipv4Mutation<'a, M = Adjusted> {
    header: &'a mut Ipv4Header,
    mode: M,
}

impl<M> Ipv4Mutation<'_, M>
where
    M: Mode,
{
    /// Sets the header length in 32-bit words and the version, 4, that
    /// shares its byte; the bits above four are dropped.
    ///
    /// The change covers this byte alone: options the new length takes in or
    /// leaves out are not summed, so a header whose options change takes its
    /// checksum anew. A packet view keeps the split it was parsed with.
    #[inline]
    pub fn set_ihl(mut self, ihl: u8) -> Self {
        let new = (4 << 4) | (ihl & 0x0f);
        let old = core::mem::replace(&mut self.header.version_ihl, new);
        self.mode.add16(mutation::first_byte(old), mutation::first_byte(new));
        self
    }

    /// Sets the DSCP; the bits above six are dropped and ECN is kept.
    #[inline]
    pub fn set_dscp(mut self, dscp: u8) -> Self {
        let old = self.header.tos;
        self.header.tos = ((dscp & 0x3f) << 2) | (old & 3);
        let new = self.header.tos;
        self.mode.add16(mutation::second_byte(old), mutation::second_byte(new));
        self
    }

    /// Sets the ECN codepoint; the bits above two are dropped.
    #[inline]
    pub fn set_ecn(mut self, ecn: u8) -> Self {
        let old = self.header.tos;
        self.header.tos = (old & 0xfc) | (ecn & 3);
        let new = self.header.tos;
        self.mode.add16(mutation::second_byte(old), mutation::second_byte(new));
        self
    }

    /// Sets the total length.
    ///
    /// A packet view keeps the split it was parsed with: after a length
    /// change, parse the packet again to see the new payload.
    #[inline]
    pub fn set_total_len(mut self, total_len: u16) -> Self {
        let old = mutation::word16(self.header.total_len);
        self.header.total_len.set(total_len);
        self.mode.add16(old, total_len.to_be());
        self
    }

    /// Sets the identification.
    #[inline]
    pub fn set_id(mut self, id: u16) -> Self {
        let old = mutation::word16(self.header.id);
        self.header.id.set(id);
        self.mode.add16(old, id.to_be());
        self
    }

    /// Sets the flags and fragment offset word.
    #[inline]
    pub fn set_frag(mut self, frag: u16) -> Self {
        let old = mutation::word16(self.header.frag);
        self.header.frag.set(frag);
        self.mode.add16(old, frag.to_be());
        self
    }

    /// Sets the TTL.
    #[inline]
    pub fn set_ttl(mut self, ttl: u8) -> Self {
        let old = core::mem::replace(&mut self.header.ttl, ttl);
        self.mode.add16(mutation::first_byte(old), mutation::first_byte(ttl));
        self
    }

    /// Sets the protocol.
    ///
    /// The L4 pseudo-header checksum covers the protocol too and is left as
    /// it was.
    #[inline]
    pub fn set_protocol(mut self, protocol: u8) -> Self {
        let old = core::mem::replace(&mut self.header.protocol, protocol);
        self.mode
            .add16(mutation::second_byte(old), mutation::second_byte(protocol));
        self
    }

    /// Sets the source address.
    #[inline]
    pub fn set_src(mut self, addr: Ipv4Addr) -> Self {
        let old = core::mem::replace(&mut self.header.src, addr.octets());
        self.mode.add_octets(old, addr.octets());
        self
    }

    /// Sets the destination address.
    #[inline]
    pub fn set_dst(mut self, addr: Ipv4Addr) -> Self {
        let old = core::mem::replace(&mut self.header.dst, addr.octets());
        self.mode.add_octets(old, addr.octets());
        self
    }
}

impl<'a> Ipv4Mutation<'a> {
    /// Ends the mutation: adjusts the header checksum once for every change.
    #[inline]
    pub fn finish(self) -> &'a mut Ipv4Header {
        let Self { header, mode } = self;
        header.checksum = mutation::adjust(header.checksum, mode);
        header
    }
}

impl<'a> Ipv4Mutation<'a, Unadjusted> {
    /// Ends the mutation and leaves the checksum to be filled.
    #[inline]
    pub fn finish_unadjusted(self) -> &'a mut Ipv4Header {
        self.header
    }
}

/// Folded sum of the header and its options.
///
/// Each part's sum is non-zero unless its bytes are, and one end-around
/// carry folds the pair, so this equals the sum of the contiguous bytes.
#[inline]
fn header_sum(header: &Ipv4Header, options: &[u8]) -> u16 {
    let sum = checksum::sum(header.as_bytes());
    if options.is_empty() {
        return sum;
    }
    let acc = u32::from(sum) + u32::from(checksum::sum(options));
    ((acc >> 16) + (acc & 0xffff)) as u16
}

/// Stores the header checksum computed anew over the header and its options.
#[inline]
fn fill_header_checksum(header: &mut Ipv4Header, options: &[u8]) {
    header.checksum = U16::ZERO;
    let sum = header_sum(header, options);
    header.checksum.set(!sum);
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
    /// Options, the bytes the header length counts past the fixed header.
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
    /// Options, the bytes the header length counts past the fixed header.
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
        fill_header_checksum(self.header, self.options);
    }
}

/// An IPv4 header without options, as a value written whole.
///
/// Writing it fills the total length and the header checksum, so what lands
/// in the frame is a header the packet parser takes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Ipv4Repr {
    /// The DSCP, its bits above six dropped.
    pub dscp: u8,
    /// The ECN codepoint, its bits above two dropped.
    pub ecn: u8,
    pub id: u16,
    /// The flags and fragment offset word, the offset in 8-byte units.
    pub frag: u16,
    pub ttl: u8,
    pub protocol: u8,
    pub src: Ipv4Addr,
    pub dst: Ipv4Addr,
}

impl Ipv4Repr {
    /// Writes the header at the start of an area, for a datagram of the given
    /// number of bytes behind it.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the area is shorter than the header,
    /// and [`ParseError::LengthBeyondField`] for a datagram the total length
    /// cannot hold; nothing is written then.
    #[inline]
    pub fn write(&self, area: &mut [u8], upper_len: usize) -> Result<(), ParseError> {
        let total = Ipv4Header::MIN_LEN
            .checked_add(upper_len)
            .and_then(|length| u16::try_from(length).ok())
            .ok_or(ParseError::LengthBeyondField {
                length: Ipv4Header::MIN_LEN.saturating_add(upper_len),
                max: usize::from(u16::MAX),
            })?;
        let (header, _) = split_header_mut::<Ipv4Header>(area)?;
        let mut value = Ipv4Header {
            version_ihl: (4 << 4) | (Ipv4Header::MIN_LEN / 4) as u8,
            tos: ((self.dscp & 0x3f) << 2) | (self.ecn & 0x03),
            total_len: U16::new(total),
            id: U16::new(self.id),
            frag: U16::new(self.frag),
            ttl: self.ttl,
            protocol: self.protocol,
            checksum: U16::ZERO,
            src: self.src.octets(),
            dst: self.dst.octets(),
        };
        let (words, _) = value.as_bytes().as_chunks::<2>();
        let sum = words.iter().map(|&word| u64::from(u16::from_be_bytes(word))).sum();
        value.checksum = U16::new(!crate::fold(sum));
        *header = value;
        Ok(())
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
        p.header.mutate().set_ttl(1).finish();
        assert_eq!(None, p.header.decrement_ttl());
        assert_eq!(1, p.header.ttl());
        assert!(p.checksum_is_valid());
        p.header.mutate().set_ttl(0).finish();
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
            p.header.mutate().set_ttl(ttl).finish();
            assert_eq!(Some(ttl - 1), p.header.decrement_ttl());
            assert_matches_recompute(&mut buf[..total]);
        }
    }

    #[test]
    fn test_ipv4_init_refuses_short_buffer() {
        let mut buf = [0u8; 20];
        for len in 0..Ipv4Header::MIN_LEN {
            let expected = ParseError::Truncated { needed: 20, available: len };
            assert_eq!(expected, Ipv4Header::init(&mut buf[..len]).unwrap_err(), "len {len}");
        }
    }

    #[test]
    fn test_ipv4_set_ihl_writes_version_four_and_drops_bits_above_four() {
        let mut buf = [0x65u8; 20];
        let (header, _) = Ipv4Header::from_prefix_mut(&mut buf).unwrap();
        let header = header.mutate_unadjusted().set_ihl(0xf6).finish_unadjusted();
        assert_eq!(6, header.ihl());
        assert_eq!(4, header.version());
    }

    /// verifies that the length's change keeps the checksum valid over a span
    /// whose added options sum to zero.
    #[test]
    fn test_ipv4_set_ihl_adjusts_checksum_for_the_length() {
        let mut buf = [0u8; 24];
        Ipv4Header::init(&mut buf).unwrap().finish_unadjusted().fill_checksum();
        Ipv4Header::from_prefix_mut(&mut buf)
            .unwrap()
            .0
            .mutate()
            .set_ihl(6)
            .finish();
        assert_eq!(0xffff, checksum::sum(&buf));
    }

    #[test]
    fn test_ipv4_from_prefix_reads_header_of_packet_beyond_input() {
        let (buf, _) = build(0, &[1, 2, 3]);
        let (header, rest) = Ipv4Header::from_prefix(&buf[..20]).unwrap();
        assert_eq!(23, header.total_len());
        assert!(rest.is_empty());
    }

    #[test]
    fn test_ipv4_from_prefix_refuses_short_input() {
        let (mut buf, _) = build(0, &[]);
        for len in 0..Ipv4Header::MIN_LEN {
            let expected = ParseError::Truncated { needed: 20, available: len };
            assert_eq!(expected, Ipv4Header::from_prefix(&buf[..len]).unwrap_err(), "len {len}");
        }
        let expected = ParseError::Truncated { needed: 20, available: 19 };
        assert_eq!(expected, Ipv4Header::from_prefix_mut(&mut buf[..19]).unwrap_err());
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

        fn read(packet: &Ipv4Packet<'_>) -> Self {
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

    /// A change of an IPv4 header field.
    #[derive(Debug, Clone)]
    enum Op {
        Dscp(u8),
        Ecn(u8),
        Id(u16),
        Frag(u16),
        Ttl(u8),
        Protocol(u8),
        /// Taken modulo the room the buffer leaves for the payload.
        TotalLen(u16),
        Src([u8; 4]),
        Dst([u8; 4]),
    }

    /// A step of a header's changes: a mutation, or the TTL's decrement,
    /// which runs its own.
    #[derive(Debug, Clone)]
    enum Step {
        Mutation(Vec<Op>),
        DecrementTtl,
    }

    /// A random change of a header field.
    fn op() -> impl Strategy<Value = Op> {
        prop_oneof![
            any::<u8>().prop_map(Op::Dscp),
            any::<u8>().prop_map(Op::Ecn),
            any::<u16>().prop_map(Op::Id),
            any::<u16>().prop_map(Op::Frag),
            any::<u8>().prop_map(Op::Ttl),
            any::<u8>().prop_map(Op::Protocol),
            any::<u16>().prop_map(Op::TotalLen),
            any::<[u8; 4]>().prop_map(Op::Src),
            any::<[u8; 4]>().prop_map(Op::Dst),
        ]
    }

    /// Random steps of header changes.
    fn steps() -> impl Strategy<Value = Vec<Step>> {
        let step = prop_oneof![
            4 => vec(op(), 0..=8).prop_map(Step::Mutation),
            1 => Just(Step::DecrementTtl),
        ];
        vec(step, 0..=6)
    }

    /// Applies the change through the mutation and to the model of the
    /// fields; the total length stays within the room the payload leaves.
    fn apply<'a, M>(mutation: Ipv4Mutation<'a, M>, op: &Op, model: &mut Fields, room: usize) -> Ipv4Mutation<'a, M>
    where
        M: Mode,
    {
        match *op {
            Op::Dscp(dscp) => {
                model.dscp = dscp & 0x3f;
                mutation.set_dscp(dscp)
            }
            Op::Ecn(ecn) => {
                model.ecn = ecn & 3;
                mutation.set_ecn(ecn)
            }
            Op::Id(id) => {
                model.id = id;
                mutation.set_id(id)
            }
            Op::Frag(frag) => {
                model.frag = frag;
                mutation.set_frag(frag)
            }
            Op::Ttl(ttl) => {
                model.ttl = ttl;
                mutation.set_ttl(ttl)
            }
            Op::Protocol(protocol) => {
                model.protocol = protocol;
                mutation.set_protocol(protocol)
            }
            Op::TotalLen(total_len) => {
                let total_len = (model.header_len() + usize::from(total_len) % room) as u16;
                model.total_len = total_len;
                mutation.set_total_len(total_len)
            }
            Op::Src(src) => {
                model.src = src;
                mutation.set_src(Ipv4Addr::from(src))
            }
            Op::Dst(dst) => {
                model.dst = dst;
                mutation.set_dst(Ipv4Addr::from(dst))
            }
        }
    }

    /// Runs the steps on a packet with a valid checksum.
    ///
    /// Returns the buffer and the model of the fields after the same
    /// changes, with the checksum as stored.
    fn run(start: &Fields, payload: &[u8], steps: &[Step]) -> (Vec<u8>, Fields) {
        let mut buf = start.to_bytes(payload);
        Ipv4PacketMut::parse(&mut buf).unwrap().recompute_checksum();
        let mut model = Ipv4Packet::parse(&buf).map(|packet| Fields::read(&packet)).unwrap();
        for step in steps {
            let header = Ipv4PacketMut::parse(&mut buf).unwrap().header;
            match step {
                Step::Mutation(ops) => {
                    let mutation = ops.iter().fold(header.mutate(), |mutation, op| {
                        apply(mutation, op, &mut model, payload.len() + 1)
                    });
                    mutation.finish();
                }
                Step::DecrementTtl => {
                    let expected = (model.ttl > 1).then(|| model.ttl - 1);
                    assert_eq!(expected, header.decrement_ttl());
                    model.ttl = expected.unwrap_or(model.ttl);
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
            ParseError::OptionLength { .. } | ParseError::LengthBeyondField { .. } => panic!("unexpected {error:?}"),
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
        fn test_ipv4_mutation_changes_only_its_fields((start, payload) in packet_fields(), steps in steps()) {
            let (buf, model) = run(&start, &payload, &steps);
            let packet = Ipv4Packet::parse(&buf).unwrap();
            prop_assert_eq!(&model, &Fields::read(&packet));
            let payload_len = usize::from(model.total_len) - start.header_len();
            prop_assert_eq!(&payload[..payload_len], packet.payload);
        }

        #[test]
        fn test_ipv4_mutation_checksum_equals_recompute((start, payload) in packet_fields(), steps in steps()) {
            let (mut buf, _) = run(&start, &payload, &steps);
            let mut packet = Ipv4PacketMut::parse(&mut buf).unwrap();
            prop_assert!(packet.checksum_is_valid());
            let incremental = packet.header.checksum();
            packet.recompute_checksum();
            prop_assert!(testing::same_checksum(incremental, packet.header.checksum()));
        }

        #[test]
        fn test_ipv4_unadjusted_mutation_stores_fields_alone(
            (start, payload) in packet_fields(),
            ops in vec(op(), 0..=16),
        ) {
            let mut buf = start.to_bytes(&payload);
            let mut model = start.clone();
            let header = Ipv4PacketMut::parse(&mut buf).unwrap().header;
            let mutation = ops
                .iter()
                .fold(header.mutate_unadjusted(), |mutation, op| apply(mutation, op, &mut model, payload.len() + 1));
            mutation.finish_unadjusted();
            let payload_len = usize::from(model.total_len) - start.header_len();
            let packet = Ipv4Packet::parse(&buf).unwrap();
            prop_assert_eq!(&model, &Fields::read(&packet));
            prop_assert_eq!(&payload[..payload_len], packet.payload);
        }

        #[test]
        fn test_ipv4_init_writes_empty_header_over_junk(junk in any::<[u8; 20]>()) {
            let mut buf = junk;
            Ipv4Header::init(&mut buf).unwrap().finish_unadjusted();
            let mut expected = [0; 20];
            expected[0] = 0x45;
            prop_assert_eq!(expected, buf);
        }

        #[test]
        fn test_ipv4_init_and_setters_read_back_fields(fields in fields(), junk in any::<[u8; 20]>()) {
            let mut buf = junk;
            Ipv4Header::init(&mut buf)
                .unwrap()
                .set_dscp(fields.dscp)
                .set_ecn(fields.ecn)
                .set_total_len(fields.total_len)
                .set_id(fields.id)
                .set_frag(fields.frag)
                .set_ttl(fields.ttl)
                .set_protocol(fields.protocol)
                .set_src(Ipv4Addr::from(fields.src))
                .set_dst(Ipv4Addr::from(fields.dst))
                .finish_unadjusted();
            let (header, _) = Ipv4Header::from_prefix(&buf).unwrap();
            prop_assert_eq!((4, 5), (header.version(), header.ihl()));
            let read = Fields {
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
                options: Vec::new(),
            };
            prop_assert_eq!(Fields { checksum: 0, options: Vec::new(), ..fields }, read);
        }

        #[test]
        fn test_ipv4_fill_checksum_validates_header_without_options(
            (start, payload) in packet_fields(),
        ) {
            let start = Fields { options: Vec::new(), ..start };
            let total_len = (start.header_len() + payload.len()) as u16;
            let mut buf = Fields { total_len, ..start }.to_bytes(&payload);
            Ipv4PacketMut::parse(&mut buf).unwrap().header.fill_checksum();
            prop_assert!(Ipv4Packet::parse(&buf).unwrap().checksum_is_valid());
        }
    }
}
