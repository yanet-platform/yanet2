use core::net::Ipv4Addr;

use zerocopy::{FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned, byteorder::network_endian::U16};

use crate::checksum;

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

fn split_lengths(header: &Ipv4Header, available: usize) -> Option<(usize, usize)> {
    if header.version() != 4 || header.ihl() < 5 {
        return None;
    }
    let header_len = header.header_len();
    let total = usize::from(header.total_len());
    if total < header_len || total > available {
        return None;
    }
    Some((header_len.saturating_sub(Ipv4Header::MIN_LEN), total - header_len))
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
    /// Refuses a version other than 4, a header length below five words or
    /// beyond the total length, and a total length beyond the input.
    pub fn parse(buf: &'a [u8]) -> Option<Self> {
        let (header, rest) = Ipv4Header::ref_from_prefix(buf).ok()?;
        let (options_len, payload_len) = split_lengths(header, buf.len())?;
        let (options, rest) = rest.split_at_checked(options_len)?;
        let payload = rest.get(..payload_len)?;
        Some(Self { header, options, payload })
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
    pub fn parse(buf: &'a mut [u8]) -> Option<Self> {
        let available = buf.len();
        let (header, rest) = Ipv4Header::mut_from_prefix(buf).ok()?;
        let (options_len, payload_len) = split_lengths(header, available)?;
        let (options, rest) = rest.split_at_mut_checked(options_len)?;
        let payload = rest.get_mut(..payload_len)?;
        Some(Self { header, options, payload })
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
    use super::*;

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
        let (buf, total) = build(0, &[1, 2, 3]);
        for len in 0..total {
            assert!(Ipv4Packet::parse(&buf[..len]).is_none(), "len {len}");
        }
        let (buf, total) = build(4, &[]);
        for len in 0..total {
            assert!(Ipv4Packet::parse(&buf[..len]).is_none(), "len {len}");
        }
        assert!(Ipv4Packet::parse(&buf[..total]).is_some());
    }

    #[test]
    fn test_ipv4_refuses_malformed_header_length() {
        let (mut buf, total) = build(0, &[1, 2, 3, 4]);
        for ihl in 0..5u8 {
            buf[0] = 0x40 | ihl;
            assert!(Ipv4Packet::parse(&buf[..total]).is_none(), "ihl {ihl}");
        }
        // Header length beyond the total length.
        buf[0] = 0x4f;
        assert!(Ipv4Packet::parse(&buf[..total]).is_none());
        // Header length beyond the buffer.
        let (mut buf, _) = build(0, &[]);
        buf[0] = 0x4f;
        buf[3] = 0x3c;
        assert!(Ipv4Packet::parse(&buf[..30]).is_none());
    }

    #[test]
    fn test_ipv4_refuses_wrong_version() {
        let (mut buf, total) = build(0, &[1, 2, 3, 4]);
        buf[0] = 0x65;
        assert!(Ipv4Packet::parse(&buf[..total]).is_none());
    }

    #[test]
    fn test_ipv4_refuses_total_len_below_header() {
        let (mut buf, total) = build(0, &[1, 2, 3, 4]);
        buf[3] = 19;
        assert!(Ipv4Packet::parse(&buf[..total]).is_none());
    }

    #[test]
    fn test_ipv4_refuses_total_len_beyond_input() {
        let (mut buf, total) = build(0, &[1, 2, 3, 4]);
        buf[3] = (total + 1) as u8;
        assert!(Ipv4Packet::parse(&buf[..total]).is_none());
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
    fn test_ipv4_incremental_checksum_after_each_mutation() {
        for opts in [0, 2] {
            let (mut buf, total) = build(opts, &[0xaa; 7]);
            for ttl in (0..=255u8).step_by(5) {
                let p = Ipv4PacketMut::parse(&mut buf[..total]).unwrap();
                p.header.set_ttl(ttl);
                assert_matches_recompute(&mut buf[..total]);
            }
            for dscp in 0..64u8 {
                let p = Ipv4PacketMut::parse(&mut buf[..total]).unwrap();
                p.header.set_dscp(dscp);
                assert_eq!(dscp, p.header.dscp());
                assert_eq!(1, p.header.ecn());
                assert_matches_recompute(&mut buf[..total]);
            }
            for ecn in 0..4u8 {
                let p = Ipv4PacketMut::parse(&mut buf[..total]).unwrap();
                p.header.set_ecn(ecn);
                assert_matches_recompute(&mut buf[..total]);
            }
            for v in [0u16, 1, 0x00ff, 0xff00, 0x7fff, 0x8000, 0xfffe, 0xffff] {
                let p = Ipv4PacketMut::parse(&mut buf[..total]).unwrap();
                p.header.set_id(v);
                p.header.set_protocol(v as u8);
                assert_matches_recompute(&mut buf[..total]);
            }
            for i in 0..=255u8 {
                let p = Ipv4PacketMut::parse(&mut buf[..total]).unwrap();
                p.header.set_src(Ipv4Addr::new(i, i.wrapping_mul(3), 255 - i, i ^ 0x5a));
                p.header.set_dst(Ipv4Addr::new(255 - i, 0, i, 255));
                assert_matches_recompute(&mut buf[..total]);
            }
            let hlen = total - 7;
            for len in (hlen..=total).rev() {
                let p = Ipv4PacketMut::parse(&mut buf[..total]).unwrap();
                p.header.set_total_len(len as u16);
                assert_matches_recompute(&mut buf[..total]);
            }
        }
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
}
