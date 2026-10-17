//! Tunnel header views.

/// Immutable GRE header view.
///
/// The dataplane decap path handles the base 4-byte header with optional
/// fields following it; this view exposes the fixed part only, matching
/// `struct rte_gre_hdr`.
#[derive(Debug)]
pub struct Gre<'a> {
    bytes: &'a [u8],
}

impl<'a> Gre<'a> {
    /// Base header size in bytes, before optional fields.
    pub const LEN: usize = 4;

    /// Borrow a GRE header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// The flags-and-version field.
    ///
    /// Bit 0x0001 marks a checksum present, 0x0008 a key present and
    /// 0x0100 a sequence number present; these decide where the optional
    /// fields sit and how large the full header is.
    pub fn flags_ver(&self) -> u16 {
        u16::from_be_bytes([self.bytes[0], self.bytes[1]])
    }

    /// The encapsulated protocol, in host byte order.
    pub fn protocol(&self) -> u16 {
        u16::from_be_bytes([self.bytes[2], self.bytes[3]])
    }

    /// Checksum-present flag.
    pub fn has_checksum(&self) -> bool {
        self.bytes[0] & 0x80 != 0
    }

    /// Key-present flag.
    pub fn has_key(&self) -> bool {
        self.bytes[0] & 0x20 != 0
    }

    /// Sequence-number-present flag.
    pub fn has_sequence(&self) -> bool {
        self.bytes[1] & 0x01 != 0
    }
}

/// Mutable GRE header view.
#[derive(Debug)]
pub struct GreMut<'a> {
    bytes: &'a mut [u8],
}

impl<'a> GreMut<'a> {
    /// Base header size in bytes, before optional fields.
    pub const LEN: usize = 4;

    /// Borrow a mutable GRE header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a mut [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// Overwrite the flags-and-version field.
    pub fn set_flags_ver(&mut self, flags_ver: u16) {
        let wire = flags_ver.to_be_bytes();
        self.bytes[0] = wire[0];
        self.bytes[1] = wire[1];
    }

    /// Overwrite the encapsulated protocol.
    pub fn set_protocol(&mut self, protocol: u16) {
        let wire = protocol.to_be_bytes();
        self.bytes[2] = wire[0];
        self.bytes[3] = wire[1];
    }
}
