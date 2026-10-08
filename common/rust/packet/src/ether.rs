//! Ethernet II, 802.1Q and ARP header views.

use crate::protocol::EtherType;

/// Ethernet II header: two addresses and the ether type.
///
/// The ethertype field is big-endian on the wire; accessors convert.
#[derive(Debug)]
pub struct Eth2<'a> {
    bytes: &'a [u8],
}

impl<'a> Eth2<'a> {
    /// Header size in bytes.
    pub const LEN: usize = 14;

    /// Borrow an Ethernet header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// The raw on-wire bytes.
    pub fn bytes(&self) -> &'a [u8] {
        &self.bytes[..Self::LEN]
    }

    /// Destination MAC address.
    pub fn dst_mac(&self) -> [u8; 6] {
        self.bytes[..6].try_into().expect("length checked")
    }

    /// Source MAC address.
    pub fn src_mac(&self) -> [u8; 6] {
        self.bytes[6..12].try_into().expect("length checked")
    }

    /// The ether type in host byte order.
    pub fn ether_type(&self) -> u16 {
        u16::from_be_bytes([self.bytes[12], self.bytes[13]])
    }

    /// The parsed ether type, when recognized.
    pub fn ether_type_known(&self) -> Option<EtherType> {
        EtherType::from_wire(self.ether_type())
    }

    /// The payload following this header.
    pub fn payload(&self) -> &'a [u8] {
        &self.bytes[Self::LEN..]
    }
}

/// Mutable Ethernet II header view.
#[derive(Debug)]
pub struct Eth2Mut<'a> {
    bytes: &'a mut [u8],
}

impl<'a> Eth2Mut<'a> {
    /// Header size in bytes.
    pub const LEN: usize = 14;

    /// Borrow a mutable Ethernet header from the start of `bytes`.
    pub fn from_bytes(bytes: &'a mut [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// Overwrite the destination MAC address.
    pub fn set_dst_mac(&mut self, mac: [u8; 6]) {
        self.bytes[..6].copy_from_slice(&mac);
    }

    /// Overwrite the source MAC address.
    pub fn set_src_mac(&mut self, mac: [u8; 6]) {
        self.bytes[6..12].copy_from_slice(&mac);
    }

    /// Overwrite the ether type.
    pub fn set_ether_type(&mut self, ether_type: u16) {
        let wire = ether_type.to_be_bytes();
        self.bytes[12] = wire[0];
        self.bytes[13] = wire[1];
    }
}

/// Single 802.1Q tag: TCI plus the encapsulated ether type.
///
/// A second tag repeats directly after the first one, so stacked VLANs
/// read as a chain of [`Vlan`] views.
#[derive(Debug)]
pub struct Vlan<'a> {
    bytes: &'a [u8],
}

impl<'a> Vlan<'a> {
    /// Tag size in bytes.
    pub const LEN: usize = 4;

    /// Borrow a VLAN tag from the start of `bytes`.
    pub fn from_bytes(bytes: &'a [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        Some(Self { bytes })
    }

    /// Priority code point (3 bits).
    pub fn pcp(&self) -> u8 {
        self.bytes[0] >> 5
    }

    /// Drop eligible indicator.
    pub fn dei(&self) -> bool {
        self.bytes[0] & 0x10 != 0
    }

    /// VLAN identifier (12 bits).
    pub fn vid(&self) -> u16 {
        u16::from_be_bytes([self.bytes[0], self.bytes[1]]) & 0x0fff
    }

    /// The encapsulated ether type in host byte order.
    pub fn ether_type(&self) -> u16 {
        u16::from_be_bytes([self.bytes[2], self.bytes[3]])
    }

    /// The payload following this tag.
    pub fn payload(&self) -> &'a [u8] {
        &self.bytes[Self::LEN..]
    }
}

/// ARP header for Ethernet/IPv4, fixed HLEN=6 PLEN=4 shape.
#[derive(Debug)]
pub struct Arp<'a> {
    bytes: &'a [u8],
}

impl<'a> Arp<'a> {
    /// Header size in bytes for the Ethernet/IPv4 shape.
    pub const LEN: usize = 28;

    /// Borrow an ARP header from the start of `bytes`.
    ///
    /// Returns `None` for a non-Ethernet/non-IPv4 hardware or protocol
    /// address length, the only shape the dataplane cares about.
    pub fn from_bytes(bytes: &'a [u8]) -> Option<Self> {
        if bytes.len() < Self::LEN {
            return None;
        }
        if bytes[4] != 6 || bytes[5] != 4 {
            return None;
        }
        Some(Self { bytes })
    }

    /// Operation: 1 request, 2 reply.
    pub fn operation(&self) -> u16 {
        u16::from_be_bytes([self.bytes[6], self.bytes[7]])
    }

    /// Sender hardware (MAC) address.
    pub fn sender_mac(&self) -> [u8; 6] {
        self.bytes[8..14].try_into().expect("length checked")
    }

    /// Sender protocol (IPv4) address.
    pub fn sender_ip(&self) -> [u8; 4] {
        self.bytes[14..18].try_into().expect("length checked")
    }

    /// Target hardware (MAC) address.
    pub fn target_mac(&self) -> [u8; 6] {
        self.bytes[18..24].try_into().expect("length checked")
    }

    /// Target protocol (IPv4) address.
    pub fn target_ip(&self) -> [u8; 4] {
        self.bytes[24..28].try_into().expect("length checked")
    }
}
