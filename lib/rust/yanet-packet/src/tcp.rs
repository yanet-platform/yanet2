use core::net::{Ipv4Addr, Ipv6Addr};

use zerocopy::{
    FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned,
    byteorder::network_endian::{U16, U32},
};

use crate::checksum;

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

fn split_options(header: &TcpHeader, available: usize) -> Option<usize> {
    let header_len = header.header_len();
    if header.data_offset() < 5 || header_len > available {
        return None;
    }
    Some(header_len - TcpHeader::MIN_LEN)
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
    /// Splits a TCP segment at the start of `buf`.
    ///
    /// Refuses a data offset below five words or beyond `buf`.
    pub fn parse(buf: &'a [u8]) -> Option<Self> {
        let (header, rest) = TcpHeader::ref_from_prefix(buf).ok()?;
        let options_len = split_options(header, buf.len())?;
        let (options, payload) = rest.split_at_checked(options_len)?;
        Some(Self { header, options, payload })
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
    pub fn parse(buf: &'a mut [u8]) -> Option<Self> {
        let available = buf.len();
        let (header, rest) = TcpHeader::mut_from_prefix(buf).ok()?;
        let options_len = split_options(header, available)?;
        let (options, payload) = rest.split_at_mut_checked(options_len)?;
        Some(Self { header, options, payload })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::IPPROTO_TCP;

    /// Builds a segment with `opts` option words and a valid checksum for the
    /// given IPv4 addresses; returns the buffer and the segment length.
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

    /// Whether the segment's checksum verifies over the IPv4 pseudo-header.
    fn valid(src: [u8; 4], dst: [u8; 4], seg: &[u8]) -> bool {
        checksum::l4_is_valid_ipv4(Ipv4Addr::from(src), Ipv4Addr::from(dst), IPPROTO_TCP, seg)
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
    fn test_tcp_refuses_short_and_malformed() {
        let (mut buf, total) = build([1, 2, 3, 4], [5, 6, 7, 8], 2, &[]);
        for len in 0..total {
            assert!(TcpPacket::parse(&buf[..len]).is_none(), "len {len}");
        }
        for offset in 0..5u8 {
            buf[12] = offset << 4;
            assert!(TcpPacket::parse(&buf[..total]).is_none(), "offset {offset}");
        }
        buf[12] = 0xf0;
        assert!(TcpPacket::parse(&buf[..total]).is_none());
        assert!(TcpPacketMut::parse(&mut buf[..total]).is_none());
    }

    #[test]
    fn test_tcp_segment_without_options_needs_twenty_bytes() {
        let (buf, _) = build([1, 2, 3, 4], [5, 6, 7, 8], 0, &[]);
        assert!(TcpPacket::parse(&buf[..20]).is_some());
        assert!(TcpPacket::parse(&buf[..19]).is_none());
    }

    #[test]
    fn test_tcp_incremental_checksum_for_ports_and_addresses() {
        for opts in [0, 3] {
            let mut src = [10, 0, 0, 1];
            let mut dst = [192, 168, 1, 9];
            let (mut buf, total) = build(src, dst, opts, &[0x55; 9]);
            assert!(valid(src, dst, &buf[..total]));
            for i in 0..=255u8 {
                let new_src = [i, i ^ 0xa5, 255 - i, i.wrapping_mul(7)];
                let new_dst = [255 - i, 0, i, 1];
                let p = TcpPacketMut::parse(&mut buf[..total]).unwrap();
                p.header.set_src_port(u16::from(i) * 257);
                p.header.set_dst_port(u16::from(255 - i) * 131);
                p.header.replace_ipv4_addr(Ipv4Addr::from(src), Ipv4Addr::from(new_src));
                p.header.replace_ipv4_addr(Ipv4Addr::from(dst), Ipv4Addr::from(new_dst));
                src = new_src;
                dst = new_dst;
                assert!(valid(src, dst, &buf[..total]), "step {i}");
                assert_ne!(0, TcpPacket::parse(&buf[..total]).unwrap().header.checksum());
            }
        }
    }

    #[test]
    fn test_tcp_incremental_checksum_for_ipv6_addresses() {
        let src = Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1);
        let dst = Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 2);
        let (mut buf, total) = build([0; 4], [0; 4], 0, &[1, 2, 3, 4]);
        buf[16] = 0;
        buf[17] = 0;
        let sum = checksum::l4_checksum_ipv6(src, dst, IPPROTO_TCP, &buf[..total]);
        buf[16..18].copy_from_slice(&sum.to_be_bytes());
        let new_src = Ipv6Addr::new(0xfd00, 1, 2, 3, 4, 5, 6, 0xffff);
        let p = TcpPacketMut::parse(&mut buf[..total]).unwrap();
        p.header.replace_ipv6_addr(src, new_src);
        assert!(checksum::l4_is_valid_ipv6(new_src, dst, IPPROTO_TCP, &buf[..total]));
    }
}
