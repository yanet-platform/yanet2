//! IPv6 header and extension header views.

use crate::protocol::IpProtocol;

/// Immutable IPv6 header view.
#[derive(Debug)]
pub struct Ipv6<'a> {
    bytes: &'a [u8],
}

impl<'a> Ipv6<'a> {
    /// Fixed header size in bytes.
    pub const LEN: usize = 40;

    /// Borrow an IPv6 header from the start of `bytes`.
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

    /// Traffic class (8 bits).
    pub fn traffic_class(&self) -> u8 {
        ((self.bytes[0] & 0x0f) << 4) | (self.bytes[1] >> 4)
    }

    /// Flow label (20 bits).
    pub fn flow_label(&self) -> u32 {
        u32::from_be_bytes([0, self.bytes[1] & 0x0f, self.bytes[2], self.bytes[3]])
    }

    /// Payload length field.
    pub fn payload_length(&self) -> u16 {
        u16::from_be_bytes([self.bytes[4], self.bytes[5]])
    }

    /// Next header protocol number.
    pub fn next_header(&self) -> u8 {
        self.bytes[6]
    }

    /// Hop limit.
    pub fn hop_limit(&self) -> u8 {
        self.bytes[7]
    }

    /// Source address.
    pub fn src(&self) -> [u8; 16] {
        self.bytes[8..24].try_into().expect("length checked")
    }

    /// Destination address.
    pub fn dst(&self) -> [u8; 16] {
        self.bytes[24..40].try_into().expect("length checked")
    }

    /// The payload following the fixed header.
    ///
    /// Extension headers are part of the payload under RFC 8200; walk
    /// them with [`Ipv6ExtIter`] when their content matters.
    pub fn payload(&self) -> &'a [u8] {
        &self.bytes[Self::LEN..]
    }
}

/// Mutable IPv6 header view.
#[derive(Debug)]
pub struct Ipv6Mut<'a> {
    bytes: &'a mut [u8],
}

impl<'a> Ipv6Mut<'a> {
    /// Fixed header size in bytes.
    pub const LEN: usize = 40;

    /// Borrow a mutable IPv6 header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a mut [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// Overwrite the payload length field.
    pub fn set_payload_length(&mut self, payload_length: u16) {
        let wire = payload_length.to_be_bytes();
        self.bytes[4] = wire[0];
        self.bytes[5] = wire[1];
    }

    /// Overwrite the next header protocol number.
    pub fn set_next_header(&mut self, next_header: u8) {
        self.bytes[6] = next_header;
    }

    /// Overwrite the hop limit.
    pub fn set_hop_limit(&mut self, hop_limit: u8) {
        self.bytes[7] = hop_limit;
    }

    /// Overwrite the source address.
    pub fn set_src(&mut self, src: [u8; 16]) {
        self.bytes[8..24].copy_from_slice(&src);
    }

    /// Overwrite the destination address.
    pub fn set_dst(&mut self, dst: [u8; 16]) {
        self.bytes[24..40].copy_from_slice(&dst);
    }
}

/// One extension header step of an IPv6 packet, borrowed by the walker.
#[derive(Debug)]
pub enum Ipv6Ext<'a> {
    /// Hop-by-hop options header.
    HopByHop(Ipv6ExtHopByHop<'a>),
    /// Routing header.
    Routing(Ipv6ExtRouting<'a>),
    /// Fragment header.
    Fragment(Ipv6ExtFragment<'a>),
    /// Destination options header.
    Destination(Ipv6ExtHopByHop<'a>),
    /// A header the walker does not decode; carried as raw bytes.
    Other(&'a [u8]),
}

impl Ipv6Ext<'_> {
    /// The next header protocol number following this extension.
    pub fn next_header(&self) -> u8 {
        match self {
            Self::HopByHop(ext) => ext.next_header(),
            Self::Routing(ext) => ext.next_header(),
            Self::Fragment(ext) => ext.next_header(),
            Self::Destination(ext) => ext.next_header(),
            Self::Other(bytes) => bytes[0],
        }
    }

    /// Total size of this extension header in bytes.
    ///
    /// The fixed part plus the length field's 8-byte units; a fragment
    /// header carries its own fixed size.
    pub fn header_len(&self) -> usize {
        match self {
            Self::HopByHop(ext) => ext.header_len(),
            Self::Routing(ext) => ext.header_len(),
            Self::Fragment(_) => Ipv6ExtFragment::LEN,
            Self::Destination(ext) => ext.header_len(),
            Self::Other(bytes) => bytes.len(),
        }
    }
}

/// Generic 2-byte-plus-options extension header (hop-by-hop and
/// destination options).
#[derive(Debug)]
pub struct Ipv6ExtHopByHop<'a> {
    bytes: &'a [u8],
}

impl<'a> Ipv6ExtHopByHop<'a> {
    /// Fixed part size in bytes.
    pub const FIXED_LEN: usize = 2;

    /// Borrow a hop-by-hop/destination options header from `bytes`.
    pub fn from_bytes(bytes: &'a [u8]) -> Option<Self> {
        if bytes.len() < Self::FIXED_LEN {
            return None;
        }
        let this = Self { bytes };
        if bytes.len() < this.header_len() {
            return None;
        }
        Some(this)
    }

    /// The protocol following this header.
    pub fn next_header(&self) -> u8 {
        self.bytes[0]
    }

    /// Total size in bytes: the length field counts 8-byte units after
    /// the first eight, matching the parser's `(1 + len) * 8`.
    pub fn header_len(&self) -> usize {
        (self.bytes[1] as usize + 1) * 8
    }

    /// The options bytes.
    pub fn options(&self) -> &'a [u8] {
        &self.bytes[..self.header_len()]
    }
}

/// Routing extension header.
#[derive(Debug)]
pub struct Ipv6ExtRouting<'a> {
    bytes: &'a [u8],
}

impl<'a> Ipv6ExtRouting<'a> {
    /// Fixed part size in bytes.
    pub const FIXED_LEN: usize = 4;

    /// Borrow a routing header from `bytes`.
    pub fn from_bytes(bytes: &'a [u8]) -> Option<Self> {
        if bytes.len() < Self::FIXED_LEN {
            return None;
        }
        let this = Self { bytes };
        if bytes.len() < this.header_len() {
            return None;
        }
        Some(this)
    }

    /// The protocol following this header.
    pub fn next_header(&self) -> u8 {
        self.bytes[0]
    }

    /// Routing type field.
    pub fn routing_type(&self) -> u8 {
        self.bytes[2]
    }

    /// Segments left field.
    pub fn segments_left(&self) -> u8 {
        self.bytes[3]
    }

    /// Total size in bytes: the length field counts 8-byte units after
    /// the first eight, like every non-fragment extension.
    pub fn header_len(&self) -> usize {
        (self.bytes[1] as usize + 1) * 8
    }

    /// The type-specific data.
    pub fn data(&self) -> &'a [u8] {
        &self.bytes[Self::FIXED_LEN..self.header_len()]
    }
}

/// Fragment extension header.
#[derive(Debug)]
pub struct Ipv6ExtFragment<'a> {
    bytes: &'a [u8],
}

impl<'a> Ipv6ExtFragment<'a> {
    /// Fixed header size in bytes.
    pub const LEN: usize = 8;

    /// Borrow a fragment header from `bytes`.
    pub fn from_bytes(bytes: &'a [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// The protocol following this header.
    pub fn next_header(&self) -> u8 {
        self.bytes[0]
    }

    /// Fragment offset in 8-byte units.
    pub fn fragment_offset(&self) -> u16 {
        u16::from_be_bytes([self.bytes[2], self.bytes[3]]) >> 3
    }

    /// More fragments flag.
    pub fn more_fragments(&self) -> bool {
        self.bytes[3] & 0x01 != 0
    }

    /// Identification field.
    pub fn identification(&self) -> u32 {
        u32::from_be_bytes([self.bytes[4], self.bytes[5], self.bytes[6], self.bytes[7]])
    }
}

/// Walker over the extension header chain of an IPv6 payload.
///
/// Starts at the payload following the fixed IPv6 header and stops at
/// the first upper-layer protocol (or an unrecognized value, which is
/// returned as [`Ipv6Ext::Other`] and ends the walk).
#[derive(Debug)]
pub struct Ipv6ExtIter<'a> {
    bytes: &'a [u8],
    next_header: u8,
}

impl<'a> Ipv6ExtIter<'a> {
    /// Start a walk over `payload` whose first header kind is named by
    /// `next_header`.
    pub fn new(payload: &'a [u8], next_header: u8) -> Self {
        Self { bytes: payload, next_header }
    }

    /// The protocol number of the header the walker stands on.
    pub fn next_header(&self) -> u8 {
        self.next_header
    }
}

impl<'a> Iterator for Ipv6ExtIter<'a> {
    type Item = Ipv6Ext<'a>;

    fn next(&mut self) -> Option<Self::Item> {
        let kind = match IpProtocol::from_raw(self.next_header) {
            Some(IpProtocol::HopByHop | IpProtocol::Routing | IpProtocol::Fragment | IpProtocol::Destination) => {
                self.next_header
            }
            _ => return None,
        };

        let ext = match kind {
            0 => Ipv6ExtHopByHop::from_bytes(self.bytes).map(Ipv6Ext::HopByHop),
            43 => Ipv6ExtRouting::from_bytes(self.bytes).map(Ipv6Ext::Routing),
            44 => Ipv6ExtFragment::from_bytes(self.bytes).map(Ipv6Ext::Fragment),
            _ => Ipv6ExtHopByHop::from_bytes(self.bytes).map(Ipv6Ext::Destination),
        };
        let ext = ext?;
        self.next_header = ext.next_header();
        self.bytes = &self.bytes[ext.header_len()..];
        Some(ext)
    }
}
