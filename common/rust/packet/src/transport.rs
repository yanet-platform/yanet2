//! TCP, UDP and ICMP header views.

/// Immutable TCP header view, fixed part without options.
#[derive(Debug)]
pub struct Tcp<'a> {
    bytes: &'a [u8],
}

impl<'a> Tcp<'a> {
    /// Fixed header size in bytes, before options.
    pub const LEN: usize = 20;

    /// Borrow a TCP header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// Source port.
    pub fn src_port(&self) -> u16 {
        u16::from_be_bytes([self.bytes[0], self.bytes[1]])
    }

    /// Destination port.
    pub fn dst_port(&self) -> u16 {
        u16::from_be_bytes([self.bytes[2], self.bytes[3]])
    }

    /// Sequence number.
    pub fn seq(&self) -> u32 {
        u32::from_be_bytes([self.bytes[4], self.bytes[5], self.bytes[6], self.bytes[7]])
    }

    /// Acknowledgement number.
    pub fn ack(&self) -> u32 {
        u32::from_be_bytes([self.bytes[8], self.bytes[9], self.bytes[10], self.bytes[11]])
    }

    /// Data offset (header length incl. options) in bytes, or `None`
    /// when the declared offset undercuts the fixed part.
    pub fn header_len(&self) -> Option<usize> {
        let words = self.bytes[12] >> 4;
        if words < 5 {
            return None;
        }
        Some(words as usize * 4)
    }

    /// The raw flags byte (NS lives in the reserved nibble).
    pub fn flags_raw(&self) -> u8 {
        self.bytes[13]
    }

    /// NS flag.
    pub fn ns(&self) -> bool {
        self.bytes[12] & 0x01 != 0
    }

    /// FIN flag.
    pub fn fin(&self) -> bool {
        self.bytes[13] & 0x01 != 0
    }

    /// SYN flag.
    pub fn syn(&self) -> bool {
        self.bytes[13] & 0x02 != 0
    }

    /// RST flag.
    pub fn rst(&self) -> bool {
        self.bytes[13] & 0x04 != 0
    }

    /// PSH flag.
    pub fn psh(&self) -> bool {
        self.bytes[13] & 0x08 != 0
    }

    /// ACK flag.
    pub fn ack_flag(&self) -> bool {
        self.bytes[13] & 0x10 != 0
    }

    /// URG flag.
    pub fn urg(&self) -> bool {
        self.bytes[13] & 0x20 != 0
    }

    /// ECE flag.
    pub fn ece(&self) -> bool {
        self.bytes[13] & 0x40 != 0
    }

    /// CWR flag.
    pub fn cwr(&self) -> bool {
        self.bytes[13] & 0x80 != 0
    }

    /// Window size.
    pub fn window(&self) -> u16 {
        u16::from_be_bytes([self.bytes[14], self.bytes[15]])
    }

    /// Checksum field.
    pub fn checksum(&self) -> u16 {
        u16::from_be_bytes([self.bytes[16], self.bytes[17]])
    }

    /// Urgent pointer.
    pub fn urgent_ptr(&self) -> u16 {
        u16::from_be_bytes([self.bytes[18], self.bytes[19]])
    }
}

/// Mutable TCP header view.
#[derive(Debug)]
pub struct TcpMut<'a> {
    bytes: &'a mut [u8],
}

impl<'a> TcpMut<'a> {
    /// Fixed header size in bytes, before options.
    pub const LEN: usize = 20;

    /// Borrow a mutable TCP header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a mut [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// Overwrite the source port.
    pub fn set_src_port(&mut self, port: u16) {
        let wire = port.to_be_bytes();
        self.bytes[0] = wire[0];
        self.bytes[1] = wire[1];
    }

    /// Overwrite the destination port.
    pub fn set_dst_port(&mut self, port: u16) {
        let wire = port.to_be_bytes();
        self.bytes[2] = wire[0];
        self.bytes[3] = wire[1];
    }

    /// Overwrite the flags byte (all but NS).
    pub fn set_flags_raw(&mut self, flags: u8) {
        self.bytes[13] = flags;
    }
}

/// Immutable UDP header view.
#[derive(Debug)]
pub struct Udp<'a> {
    bytes: &'a [u8],
}

impl<'a> Udp<'a> {
    /// Header size in bytes.
    pub const LEN: usize = 8;

    /// Borrow a UDP header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// Source port.
    pub fn src_port(&self) -> u16 {
        u16::from_be_bytes([self.bytes[0], self.bytes[1]])
    }

    /// Destination port.
    pub fn dst_port(&self) -> u16 {
        u16::from_be_bytes([self.bytes[2], self.bytes[3]])
    }

    /// Length field, header plus payload.
    pub fn length(&self) -> u16 {
        u16::from_be_bytes([self.bytes[4], self.bytes[5]])
    }

    /// Checksum field.
    pub fn checksum(&self) -> u16 {
        u16::from_be_bytes([self.bytes[6], self.bytes[7]])
    }

    /// The payload following this header.
    pub fn payload(&self) -> &'a [u8] {
        &self.bytes[Self::LEN..]
    }
}

/// Mutable UDP header view.
#[derive(Debug)]
pub struct UdpMut<'a> {
    bytes: &'a mut [u8],
}

impl<'a> UdpMut<'a> {
    /// Header size in bytes.
    pub const LEN: usize = 8;

    /// Borrow a mutable UDP header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a mut [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// Overwrite the source port.
    pub fn set_src_port(&mut self, port: u16) {
        let wire = port.to_be_bytes();
        self.bytes[0] = wire[0];
        self.bytes[1] = wire[1];
    }

    /// Overwrite the destination port.
    pub fn set_dst_port(&mut self, port: u16) {
        let wire = port.to_be_bytes();
        self.bytes[2] = wire[0];
        self.bytes[3] = wire[1];
    }

    /// Overwrite the length field.
    pub fn set_length(&mut self, length: u16) {
        let wire = length.to_be_bytes();
        self.bytes[4] = wire[0];
        self.bytes[5] = wire[1];
    }

    /// Overwrite the checksum field.
    pub fn set_checksum(&mut self, checksum: u16) {
        let wire = checksum.to_be_bytes();
        self.bytes[6] = wire[0];
        self.bytes[7] = wire[1];
    }
}

/// Immutable ICMP/ICMPv6 header view.
///
/// Both protocols share the 8-byte shape: type, code, checksum and one
/// 32-bit rest-of-header word.
#[derive(Debug)]
pub struct Icmp<'a> {
    bytes: &'a [u8],
}

impl<'a> Icmp<'a> {
    /// Header size in bytes.
    pub const LEN: usize = 8;

    /// Borrow an ICMP header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// Message type.
    pub fn icmp_type(&self) -> u8 {
        self.bytes[0]
    }

    /// Message code.
    pub fn code(&self) -> u8 {
        self.bytes[1]
    }

    /// Checksum field.
    pub fn checksum(&self) -> u16 {
        u16::from_be_bytes([self.bytes[2], self.bytes[3]])
    }

    /// The rest-of-header word (identifier/sequence, or a pointer).
    pub fn rest(&self) -> u32 {
        u32::from_be_bytes([self.bytes[4], self.bytes[5], self.bytes[6], self.bytes[7]])
    }

    /// The message payload following the header.
    pub fn payload(&self) -> &'a [u8] {
        &self.bytes[Self::LEN..]
    }
}

/// Mutable ICMP/ICMPv6 header view.
#[derive(Debug)]
pub struct IcmpMut<'a> {
    bytes: &'a mut [u8],
}

impl<'a> IcmpMut<'a> {
    /// Header size in bytes.
    pub const LEN: usize = 8;

    /// Borrow a mutable ICMP header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a mut [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// Overwrite the message type.
    pub fn set_icmp_type(&mut self, icmp_type: u8) {
        self.bytes[0] = icmp_type;
    }

    /// Overwrite the message code.
    pub fn set_code(&mut self, code: u8) {
        self.bytes[1] = code;
    }

    /// Overwrite the checksum field.
    pub fn set_checksum(&mut self, checksum: u16) {
        let wire = checksum.to_be_bytes();
        self.bytes[2] = wire[0];
        self.bytes[3] = wire[1];
    }
}
