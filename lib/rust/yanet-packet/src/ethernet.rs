use netip::MacAddr;
use zerocopy::{FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned, byteorder::network_endian::U16};

pub const ETHERTYPE_IPV4: u16 = 0x0800;
pub const ETHERTYPE_IPV6: u16 = 0x86dd;
pub const ETHERTYPE_VLAN: u16 = 0x8100;
pub const ETHERTYPE_QINQ: u16 = 0x88a8;

/// Ethernet II header.
#[derive(Debug, Clone, Copy, PartialEq, Eq, FromBytes, IntoBytes, KnownLayout, Immutable, Unaligned)]
#[repr(C)]
pub struct EthernetHeader {
    dst: [u8; 6],
    src: [u8; 6],
    ethertype: U16,
}

impl EthernetHeader {
    pub const LEN: usize = 14;

    /// Splits the header off the front of the input.
    pub fn parse(buf: &[u8]) -> Option<(&Self, &[u8])> {
        Self::ref_from_prefix(buf).ok()
    }

    /// Splits the header off the front of the input for in-place editing.
    pub fn parse_mut(buf: &mut [u8]) -> Option<(&mut Self, &mut [u8])> {
        Self::mut_from_prefix(buf).ok()
    }

    pub fn dst(&self) -> MacAddr {
        MacAddr::from(self.dst)
    }

    pub fn src(&self) -> MacAddr {
        MacAddr::from(self.src)
    }

    pub fn set_dst(&mut self, addr: MacAddr) {
        self.dst = addr.octets();
    }

    pub fn set_src(&mut self, addr: MacAddr) {
        self.src = addr.octets();
    }

    pub fn ethertype(&self) -> u16 {
        self.ethertype.get()
    }

    pub fn set_ethertype(&mut self, ethertype: u16) {
        self.ethertype.set(ethertype);
    }

    /// True when a VLAN tag follows, for either tag protocol identifier.
    pub fn has_vlan(&self) -> bool {
        matches!(self.ethertype(), ETHERTYPE_VLAN | ETHERTYPE_QINQ)
    }
}

/// 802.1Q tag following an Ethernet header whose ethertype is a tag type.
#[derive(Debug, Clone, Copy, PartialEq, Eq, FromBytes, IntoBytes, KnownLayout, Immutable, Unaligned)]
#[repr(C)]
pub struct VlanTag {
    tci: U16,
    ethertype: U16,
}

impl VlanTag {
    pub const LEN: usize = 4;

    /// Splits the tag off the front of the input.
    pub fn parse(buf: &[u8]) -> Option<(&Self, &[u8])> {
        Self::ref_from_prefix(buf).ok()
    }

    /// Splits the tag off the front of the input for in-place editing.
    pub fn parse_mut(buf: &mut [u8]) -> Option<(&mut Self, &mut [u8])> {
        Self::mut_from_prefix(buf).ok()
    }

    /// Priority code point, 0 to 7.
    pub fn priority(&self) -> u8 {
        (self.tci.get() >> 13) as u8
    }

    pub fn drop_eligible(&self) -> bool {
        self.tci.get() & 0x1000 != 0
    }

    /// VLAN identifier, 0 to 4095.
    pub fn vlan_id(&self) -> u16 {
        self.tci.get() & 0x0fff
    }

    /// Ethertype of the payload that follows the tag.
    pub fn ethertype(&self) -> u16 {
        self.ethertype.get()
    }

    /// Sets the VLAN identifier; the bits above twelve are dropped.
    pub fn set_vlan_id(&mut self, vlan_id: u16) {
        self.tci.set((self.tci.get() & 0xf000) | (vlan_id & 0x0fff));
    }

    /// Sets the priority code point; the bits above three are dropped.
    pub fn set_priority(&mut self, priority: u8) {
        self.tci
            .set((self.tci.get() & 0x1fff) | (u16::from(priority & 7) << 13));
    }

    pub fn set_ethertype(&mut self, ethertype: u16) {
        self.ethertype.set(ethertype);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const FRAME: [u8; 22] = [
        1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 0x81, 0x00, 0xa1, 0x23, 0x08, 0x00, 0xde, 0xad, 0xbe, 0xef,
    ];

    #[test]
    fn test_ethernet_parse_fields() {
        let (eth, rest) = EthernetHeader::parse(&FRAME).unwrap();
        assert_eq!(MacAddr::new(1, 2, 3, 4, 5, 6), eth.dst());
        assert_eq!(MacAddr::new(7, 8, 9, 10, 11, 12), eth.src());
        assert_eq!(ETHERTYPE_VLAN, eth.ethertype());
        assert!(eth.has_vlan());
        assert_eq!(8, rest.len());
    }

    #[test]
    fn test_ethernet_set_addresses_writes_frame_bytes() {
        let mut frame = FRAME;
        let (eth, _) = EthernetHeader::parse_mut(&mut frame).unwrap();
        eth.set_dst(MacAddr::new(0xa, 0xb, 0xc, 0xd, 0xe, 0xf));
        eth.set_src(MacAddr::new(0x10, 0x11, 0x12, 0x13, 0x14, 0x15));
        assert_eq!(
            [0xa, 0xb, 0xc, 0xd, 0xe, 0xf, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15],
            frame[..12]
        );
        assert_eq!(FRAME[12..], frame[12..]);
    }

    #[test]
    fn test_ethernet_refuses_short_buffer() {
        for len in 0..EthernetHeader::LEN {
            assert!(EthernetHeader::parse(&FRAME[..len]).is_none(), "len {len}");
        }
        let mut copy = FRAME;
        assert!(EthernetHeader::parse_mut(&mut copy[..13]).is_none());
    }

    #[test]
    fn test_ethernet_set_ethertype_in_place() {
        let mut copy = FRAME;
        let (eth, _) = EthernetHeader::parse_mut(&mut copy).unwrap();
        eth.set_ethertype(ETHERTYPE_IPV6);
        assert!(!eth.has_vlan());
        assert_eq!([0x86, 0xdd], [copy[12], copy[13]]);
    }

    #[test]
    fn test_vlan_parse_fields() {
        let (vlan, rest) = VlanTag::parse(&FRAME[14..]).unwrap();
        assert_eq!(5, vlan.priority());
        assert!(!vlan.drop_eligible());
        assert_eq!(0x123, vlan.vlan_id());
        assert_eq!(ETHERTYPE_IPV4, vlan.ethertype());
        assert_eq!([0xde, 0xad, 0xbe, 0xef], rest);
    }

    #[test]
    fn test_vlan_refuses_short_buffer() {
        for len in 0..VlanTag::LEN {
            assert!(VlanTag::parse(&FRAME[14..14 + len]).is_none(), "len {len}");
        }
    }

    #[test]
    fn test_vlan_setters_keep_other_bits() {
        let mut copy = FRAME;
        let (vlan, _) = VlanTag::parse_mut(&mut copy[14..]).unwrap();
        vlan.set_vlan_id(0x1001);
        assert_eq!(0x001, vlan.vlan_id());
        assert_eq!(5, vlan.priority());
        vlan.set_priority(0xff);
        assert_eq!(7, vlan.priority());
        assert_eq!(0x001, vlan.vlan_id());
        assert!(!vlan.drop_eligible());
    }
}
