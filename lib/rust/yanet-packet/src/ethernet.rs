use netip::MacAddr;
use zerocopy::{FromBytes, Immutable, IntoBytes, KnownLayout, Unaligned, byteorder::network_endian::U16};

use crate::{ParseError, split_header, split_header_mut};

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
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the input is shorter than the header.
    pub fn parse(buf: &[u8]) -> Result<(&Self, &[u8]), ParseError> {
        split_header(buf)
    }

    /// Splits the header off the front of the input for in-place editing.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the input is shorter than the header.
    pub fn parse_mut(buf: &mut [u8]) -> Result<(&mut Self, &mut [u8]), ParseError> {
        split_header_mut(buf)
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
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the input is shorter than the tag.
    pub fn parse(buf: &[u8]) -> Result<(&Self, &[u8]), ParseError> {
        split_header(buf)
    }

    /// Splits the tag off the front of the input for in-place editing.
    ///
    /// # Errors
    ///
    /// [`ParseError::Truncated`] when the input is shorter than the tag.
    pub fn parse_mut(buf: &mut [u8]) -> Result<(&mut Self, &mut [u8]), ParseError> {
        split_header_mut(buf)
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

    use proptest::{collection::vec, prelude::*};

    use super::*;
    use crate::testing;

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
            let expected = ParseError::Truncated { needed: 14, available: len };
            assert_eq!(expected, EthernetHeader::parse(&FRAME[..len]).unwrap_err(), "len {len}");
        }
        let mut copy = FRAME;
        let expected = ParseError::Truncated { needed: 14, available: 13 };
        assert_eq!(expected, EthernetHeader::parse_mut(&mut copy[..13]).unwrap_err());
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
            let expected = ParseError::Truncated { needed: 4, available: len };
            assert_eq!(expected, VlanTag::parse(&FRAME[14..14 + len]).unwrap_err(), "len {len}");
        }
        let mut copy = FRAME;
        let expected = ParseError::Truncated { needed: 4, available: 3 };
        assert_eq!(expected, VlanTag::parse_mut(&mut copy[14..17]).unwrap_err());
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

    /// Ethernet header fields as built and read back.
    #[derive(Debug, Clone, PartialEq, Eq)]
    struct EthFields {
        dst: [u8; 6],
        src: [u8; 6],
        ethertype: u16,
    }

    impl EthFields {
        fn to_bytes(&self, payload: &[u8]) -> Vec<u8> {
            [&self.dst[..], &self.src, &self.ethertype.to_be_bytes(), payload].concat()
        }

        fn read(header: &EthernetHeader) -> Self {
            Self {
                dst: header.dst().octets(),
                src: header.src().octets(),
                ethertype: header.ethertype(),
            }
        }
    }

    /// Random Ethernet header fields.
    fn eth_fields() -> impl Strategy<Value = EthFields> {
        (any::<[u8; 6]>(), any::<[u8; 6]>(), any::<u16>()).prop_map(|(dst, src, ethertype)| EthFields {
            dst,
            src,
            ethertype,
        })
    }

    /// VLAN tag fields as built and read back.
    #[derive(Debug, Clone, PartialEq, Eq)]
    struct VlanFields {
        priority: u8,
        drop_eligible: bool,
        vlan_id: u16,
        ethertype: u16,
    }

    impl VlanFields {
        fn to_bytes(&self, payload: &[u8]) -> Vec<u8> {
            let tci = u16::from(self.priority) << 13 | u16::from(self.drop_eligible) << 12 | self.vlan_id;
            [&tci.to_be_bytes()[..], &self.ethertype.to_be_bytes(), payload].concat()
        }

        fn read(tag: &VlanTag) -> Self {
            Self {
                priority: tag.priority(),
                drop_eligible: tag.drop_eligible(),
                vlan_id: tag.vlan_id(),
                ethertype: tag.ethertype(),
            }
        }
    }

    /// Random VLAN tag fields.
    fn vlan_fields() -> impl Strategy<Value = VlanFields> {
        (0u8..8, any::<bool>(), 0u16..4096, any::<u16>()).prop_map(|(priority, drop_eligible, vlan_id, ethertype)| {
            VlanFields {
                priority,
                drop_eligible,
                vlan_id,
                ethertype,
            }
        })
    }

    proptest! {
        #![proptest_config(testing::config())]

        #[test]
        fn test_ethernet_round_trip_reads_back_built_fields(fields in eth_fields(), payload in vec(any::<u8>(), 0..=32)) {
            let buf = fields.to_bytes(&payload);
            let (header, rest) = EthernetHeader::parse(&buf).unwrap();
            prop_assert_eq!(&fields, &EthFields::read(header));
            prop_assert_eq!(&payload[..], rest);
        }

        #[test]
        fn test_ethernet_setters_change_only_their_field(
            start in eth_fields(),
            dst in any::<[u8; 6]>(),
            src in any::<[u8; 6]>(),
            ethertype in any::<u16>(),
        ) {
            let mut buf = start.to_bytes(&[1, 2, 3]);
            let (header, _) = EthernetHeader::parse_mut(&mut buf).unwrap();
            let mut model = start.clone();
            header.set_dst(MacAddr::from(dst));
            model.dst = dst;
            prop_assert_eq!(&model, &EthFields::read(header));
            header.set_src(MacAddr::from(src));
            model.src = src;
            prop_assert_eq!(&model, &EthFields::read(header));
            header.set_ethertype(ethertype);
            model.ethertype = ethertype;
            let (header, rest) = EthernetHeader::parse(&buf).unwrap();
            prop_assert_eq!(&model, &EthFields::read(header));
            prop_assert_eq!(&[1, 2, 3][..], rest);
        }

        #[test]
        fn test_vlan_round_trip_reads_back_built_fields(fields in vlan_fields(), payload in vec(any::<u8>(), 0..=8)) {
            let buf = fields.to_bytes(&payload);
            let (tag, rest) = VlanTag::parse(&buf).unwrap();
            prop_assert_eq!(&fields, &VlanFields::read(tag));
            prop_assert_eq!(&payload[..], rest);
        }

        #[test]
        fn test_vlan_setters_change_only_their_field(
            start in vlan_fields(),
            vlan_id in any::<u16>(),
            priority in any::<u8>(),
            ethertype in any::<u16>(),
        ) {
            let mut buf = start.to_bytes(&[]);
            let (tag, _) = VlanTag::parse_mut(&mut buf).unwrap();
            let mut model = start.clone();
            tag.set_vlan_id(vlan_id);
            model.vlan_id = vlan_id & 0x0fff;
            prop_assert_eq!(&model, &VlanFields::read(tag));
            tag.set_priority(priority);
            model.priority = priority & 7;
            prop_assert_eq!(&model, &VlanFields::read(tag));
            tag.set_ethertype(ethertype);
            model.ethertype = ethertype;
            let (tag, _) = VlanTag::parse(&buf).unwrap();
            prop_assert_eq!(&model, &VlanFields::read(tag));
        }
    }
}
