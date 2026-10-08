//! IPv4 header views.

/// Immutable IPv4 header view.
///
/// The view covers the fixed 20-byte part; options are reachable through
/// [`Ipv4::options`] and the payload starts at the full header length.
#[derive(Debug)]
pub struct Ipv4<'a> {
    bytes: &'a [u8],
}

impl<'a> Ipv4<'a> {
    /// Fixed header size in bytes, before options.
    pub const LEN: usize = 20;

    /// Borrow an IPv4 header from the start of `bytes`.
    ///
    /// Only the fixed part is length-checked here; the declared header
    /// length must be validated separately against the buffer when
    /// options or the payload are read.
    pub fn from_bytes(bytes: &'a [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// The raw on-wire bytes of the fixed header.
    pub fn bytes(&self) -> &'a [u8] {
        &self.bytes[..Self::LEN]
    }

    /// Internet header length in bytes, including options.
    ///
    /// The IHL field counts 32-bit words; a value below 5 is corrupt and
    /// yields a header length below the fixed part, so it is reported as
    /// `None`.
    pub fn header_len(&self) -> Option<usize> {
        let words = self.bytes[0] & 0x0f;
        if words < 5 {
            return None;
        }
        Some(words as usize * 4)
    }

    /// Type of service / DSCP+ECN byte.
    pub fn tos(&self) -> u8 {
        self.bytes[1]
    }

    /// Total length field, header plus payload.
    pub fn total_length(&self) -> u16 {
        u16::from_be_bytes([self.bytes[2], self.bytes[3]])
    }

    /// Identification field.
    pub fn identification(&self) -> u16 {
        u16::from_be_bytes([self.bytes[4], self.bytes[5]])
    }

    /// True when either the more-fragments bit is set or the fragment
    /// offset is nonzero, matching the parser's fragmented-packet rule.
    pub fn is_fragmented(&self) -> bool {
        let f = u16::from_be_bytes([self.bytes[6], self.bytes[7]]);
        f & 0x3fff != 0
    }

    /// Fragment offset in 8-byte units.
    pub fn fragment_offset(&self) -> u16 {
        let f = u16::from_be_bytes([self.bytes[6], self.bytes[7]]);
        f & 0x1fff
    }

    /// More fragments flag.
    pub fn more_fragments(&self) -> bool {
        self.bytes[6] & 0x20 != 0
    }

    /// Time to live.
    pub fn ttl(&self) -> u8 {
        self.bytes[8]
    }

    /// Protocol number.
    pub fn protocol(&self) -> u8 {
        self.bytes[9]
    }

    /// Header checksum field as stored on the wire.
    pub fn header_checksum(&self) -> u16 {
        u16::from_be_bytes([self.bytes[10], self.bytes[11]])
    }

    /// Source address.
    pub fn src(&self) -> [u8; 4] {
        self.bytes[12..16].try_into().expect("length checked")
    }

    /// Destination address.
    pub fn dst(&self) -> [u8; 4] {
        self.bytes[16..20].try_into().expect("length checked")
    }

    /// The options bytes between the fixed header and the payload.
    ///
    /// `None` when the declared IHL exceeds the buffer or is corrupt.
    pub fn options(&self) -> Option<&'a [u8]> {
        let header_len = self.header_len()?;
        if self.bytes.len() < header_len {
            return None;
        }
        Some(&self.bytes[Self::LEN..header_len])
    }

    /// The payload following the full header.
    ///
    /// `None` when the declared IHL exceeds the buffer or is corrupt.
    pub fn payload(&self) -> Option<&'a [u8]> {
        let header_len = self.header_len()?;
        if self.bytes.len() < header_len {
            return None;
        }
        Some(&self.bytes[header_len..])
    }
}

/// Mutable IPv4 header view.
#[derive(Debug)]
pub struct Ipv4Mut<'a> {
    bytes: &'a mut [u8],
}

impl<'a> Ipv4Mut<'a> {
    /// Fixed header size in bytes, before options.
    pub const LEN: usize = 20;

    /// Borrow a mutable IPv4 header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a mut [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// Overwrite the type of service byte.
    pub fn set_tos(&mut self, tos: u8) {
        self.bytes[1] = tos;
    }

    /// Overwrite the total length field.
    pub fn set_total_length(&mut self, total_length: u16) {
        let wire = total_length.to_be_bytes();
        self.bytes[2] = wire[0];
        self.bytes[3] = wire[1];
    }

    /// Overwrite time to live.
    pub fn set_ttl(&mut self, ttl: u8) {
        self.bytes[8] = ttl;
    }

    /// Overwrite the protocol number.
    pub fn set_protocol(&mut self, protocol: u8) {
        self.bytes[9] = protocol;
    }

    /// Overwrite the header checksum field.
    pub fn set_header_checksum(&mut self, checksum: u16) {
        let wire = checksum.to_be_bytes();
        self.bytes[10] = wire[0];
        self.bytes[11] = wire[1];
    }

    /// Overwrite the source address.
    pub fn set_src(&mut self, src: [u8; 4]) {
        self.bytes[12..16].copy_from_slice(&src);
    }

    /// Overwrite the destination address.
    pub fn set_dst(&mut self, dst: [u8; 4]) {
        self.bytes[16..20].copy_from_slice(&dst);
    }
}
