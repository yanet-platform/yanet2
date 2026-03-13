use core::{
    fmt::{self, Display, Formatter},
    str::FromStr,
};

/// A MAC address.
///
/// ```
/// use netip::MacAddr;
///
/// let mac = MacAddr::new(0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9);
/// assert_eq!("3a:ac:26:9b:5b:f9", mac.to_string());
///
/// let parsed: MacAddr = "3a:ac:26:9b:5b:f9".parse().unwrap();
/// assert_eq!(mac, parsed);
/// ```
#[derive(Debug, Clone, Copy, PartialEq, Eq, Ord, PartialOrd, Default, Hash)]
pub struct MacAddr(u64);

impl MacAddr {
    /// Creates a new MAC address from its 6 octets in transmission order.
    ///
    /// Each parameter represents one byte of the MAC address in the standard
    /// format `xx:xx:xx:xx:xx:xx`.
    #[inline]
    pub const fn new(a: u8, b: u8, c: u8, d: u8, e: u8, f: u8) -> MacAddr {
        Self(u64::from_be_bytes([a, b, c, d, e, f, 0, 0]))
    }

    /// Parses a MAC address from an ASCII byte slice.
    ///
    /// | Format           | Example             |
    /// |------------------|---------------------|
    /// | Colon-separated  | `xx:xx:xx:xx:xx:xx` |
    /// | Hyphen-separated | `xx-xx-xx-xx-xx-xx` |
    /// | No separator     | `xxxxxxxxxxxx`      |
    ///
    /// ```
    /// use netip::MacAddr;
    ///
    /// let mac = MacAddr::parse_ascii(b"3a:ac:26:9b:5b:f9").unwrap();
    /// assert_eq!(mac, MacAddr::parse_ascii(b"3a-ac-26-9b-5b-f9").unwrap());
    /// assert_eq!(mac, MacAddr::parse_ascii(b"3aac269b5bf9").unwrap());
    /// ```
    pub fn parse_ascii(b: &[u8]) -> Result<Self, MacAddrParseError> {
        match b.len() {
            17 => parse_separated(b),
            12 => parse_bare(b),
            _ => Err(MacAddrParseError),
        }
    }

    /// Returns the MAC address as a u64 value.
    #[inline]
    pub const fn as_u64(&self) -> u64 {
        match self {
            Self(addr) => *addr,
        }
    }

    /// Returns the 6 octets of the MAC address.
    #[inline]
    pub const fn octets(&self) -> [u8; 6] {
        let [a, b, c, d, e, f, ..] = self.as_u64().to_be_bytes();
        [a, b, c, d, e, f]
    }

    /// Returns `true` if all octets are zero.
    #[inline]
    pub const fn is_zero(&self) -> bool {
        self.as_u64() == 0
    }

    /// Returns `true` if this is a multicast address.
    ///
    /// ```
    /// use netip::MacAddr;
    ///
    /// assert!(MacAddr::new(0x01, 0x00, 0x5e, 0x00, 0x00, 0x01).is_multicast());
    /// assert!(!MacAddr::new(0x00, 0x1a, 0x2b, 0x3c, 0x4d, 0x5e).is_multicast());
    /// ```
    #[inline]
    pub const fn is_multicast(&self) -> bool {
        (self.as_u64() >> 56) & 0x01 != 0
    }

    /// Returns `true` if this is a locally administered address.
    #[inline]
    pub const fn is_locally_administered(&self) -> bool {
        (self.as_u64() >> 56) & 0x02 != 0
    }

    /// Returns `true` if this is the broadcast address (`ff:ff:ff:ff:ff:ff`).
    #[inline]
    pub const fn is_broadcast(&self) -> bool {
        self.as_u64() == 0xffff_ffff_ffff_0000
    }
}

impl From<u64> for MacAddr {
    /// Creates a MAC address from a u64 wire-format value.
    ///
    /// The lower 16 bits are masked off to guarantee EUI-48 semantics.
    #[inline]
    fn from(addr: u64) -> MacAddr {
        MacAddr(addr & !0xffff)
    }
}

impl From<[u8; 6]> for MacAddr {
    #[inline]
    fn from(octets: [u8; 6]) -> MacAddr {
        MacAddr::new(octets[0], octets[1], octets[2], octets[3], octets[4], octets[5])
    }
}

impl Display for MacAddr {
    fn fmt(&self, fmt: &mut Formatter) -> Result<(), fmt::Error> {
        let [a, b, c, d, e, f, ..] = self.as_u64().to_be_bytes();

        write!(fmt, "{a:02x}:{b:02x}:{c:02x}:{d:02x}:{e:02x}:{f:02x}")
    }
}

impl FromStr for MacAddr {
    type Err = MacAddrParseError;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Self::parse_ascii(s.as_bytes())
    }
}

/// Decodes a single hex digit.
const fn decode_hex(b: u8) -> Result<u8, MacAddrParseError> {
    match b {
        b'0'..=b'9' => Ok(b - b'0'),
        b'a'..=b'f' => Ok(b - b'a' + 10),
        b'A'..=b'F' => Ok(b - b'A' + 10),
        _ => Err(MacAddrParseError),
    }
}

/// Decodes two hex digits into a byte.
const fn decode_hex_pair(hi: u8, lo: u8) -> Result<u8, MacAddrParseError> {
    // NOTE: no "?", because of constant function.
    let hi = match decode_hex(hi) {
        Ok(v) => v,
        Err(e) => return Err(e),
    };
    let lo = match decode_hex(lo) {
        Ok(v) => v,
        Err(e) => return Err(e),
    };
    Ok(hi << 4 | lo)
}

/// Parses `xx:xx:xx:xx:xx:xx` or `xx-xx-xx-xx-xx-xx` from a 17-byte slice.
fn parse_separated(b: &[u8]) -> Result<MacAddr, MacAddrParseError> {
    let sep = b[2];
    if sep != b':' && sep != b'-' {
        return Err(MacAddrParseError);
    }

    let mut octets = [0u8; 6];
    for (i, octet) in octets.iter_mut().enumerate() {
        let off = i * 3;
        if i > 0 && b[off - 1] != sep {
            return Err(MacAddrParseError);
        }
        *octet = decode_hex_pair(b[off], b[off + 1])?;
    }

    Ok(MacAddr::from(octets))
}

/// Parses 12 hex digits without separators from a 12-byte slice.
fn parse_bare(b: &[u8]) -> Result<MacAddr, MacAddrParseError> {
    let mut octets = [0u8; 6];
    for i in 0..6 {
        octets[i] = decode_hex_pair(b[i * 2], b[i * 2 + 1])?;
    }

    Ok(MacAddr::from(octets))
}

/// Error returned when parsing a MAC address string fails.
#[derive(Debug, Clone, Copy)]
pub struct MacAddrParseError;

impl Display for MacAddrParseError {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        write!(f, "invalid MAC address format, expected EUI-48: xx:xx:xx:xx:xx:xx")
    }
}

impl core::error::Error for MacAddrParseError {}

#[cfg(test)]
mod test {
    use super::*;

    const MAC: MacAddr = MacAddr::new(0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9);
    const MAC_U64: u64 = 0x3aac269b5bf9_0000;

    #[test]
    fn as_u64() {
        assert_eq!(MAC_U64, MAC.as_u64());
    }

    #[test]
    fn from_u64_masks_lower_bits() {
        let mac = MacAddr::from(0x3aac269b5bf9_abcd);

        assert_eq!(MAC, mac);
        assert_eq!(MAC_U64, mac.as_u64());
    }

    #[test]
    fn from_octets() {
        assert_eq!(MAC, MacAddr::from([0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9]));
    }

    #[test]
    fn octets_roundtrip() {
        assert_eq!([0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9], MAC.octets());
    }

    #[test]
    fn display_canonical() {
        assert_eq!("3a:ac:26:9b:5b:f9", MAC.to_string());
    }

    #[test]
    fn display_zero() {
        assert_eq!("00:00:00:00:00:00", MacAddr::default().to_string());
    }

    #[test]
    fn parse_colon() {
        assert_eq!(MAC, "3a:ac:26:9b:5b:f9".parse::<MacAddr>().unwrap());
    }

    #[test]
    fn parse_colon_uppercase() {
        assert_eq!(MAC, "3A:AC:26:9B:5B:F9".parse::<MacAddr>().unwrap());
    }

    #[test]
    fn parse_colon_mixed_case() {
        assert_eq!(MAC, "3a:AC:26:9b:5B:f9".parse::<MacAddr>().unwrap());
    }

    #[test]
    fn parse_hyphen() {
        assert_eq!(MAC, "3a-ac-26-9b-5b-f9".parse::<MacAddr>().unwrap());
    }

    #[test]
    fn parse_bare() {
        assert_eq!(MAC, "3aac269b5bf9".parse::<MacAddr>().unwrap());
    }

    #[test]
    fn parse_display_roundtrip() {
        let s = "3a:ac:26:9b:5b:f9";
        let mac: MacAddr = s.parse().unwrap();

        assert_eq!(s, mac.to_string());
    }

    #[test]
    fn parse_empty() {
        assert!("".parse::<MacAddr>().is_err());
    }

    #[test]
    fn parse_colon_too_few() {
        assert!("3a:ac:26:9b:5b".parse::<MacAddr>().is_err());
    }

    #[test]
    fn parse_colon_too_many() {
        assert!("3a:ac:26:9b:5b:f9:00".parse::<MacAddr>().is_err());
    }

    #[test]
    fn parse_colon_invalid_hex() {
        assert!("3a:ac:26:zz:5b:f9".parse::<MacAddr>().is_err());
    }

    #[test]
    fn parse_bare_too_short() {
        assert!("3aac269b5bf".parse::<MacAddr>().is_err());
    }

    #[test]
    fn parse_bare_too_long() {
        assert!("3aac269b5bf900".parse::<MacAddr>().is_err());
    }

    #[test]
    fn parse_bare_invalid_hex() {
        assert!("3aac269b5bfz".parse::<MacAddr>().is_err());
    }

    #[test]
    fn is_multicast() {
        assert!(!MacAddr::new(0x00, 0x11, 0x22, 0x33, 0x44, 0x55).is_multicast());
        assert!(MacAddr::new(0x01, 0x11, 0x22, 0x33, 0x44, 0x55).is_multicast());
    }

    #[test]
    fn broadcast_is_multicast() {
        let broadcast = MacAddr::new(0xff, 0xff, 0xff, 0xff, 0xff, 0xff);
        assert!(broadcast.is_multicast());
        assert!(broadcast.is_broadcast());
    }

    #[test]
    fn is_locally_administered() {
        assert!(!MacAddr::new(0x00, 0x11, 0x22, 0x33, 0x44, 0x55).is_locally_administered());
        assert!(MacAddr::new(0x02, 0x11, 0x22, 0x33, 0x44, 0x55).is_locally_administered());
    }

    #[test]
    fn is_zero() {
        assert!(MacAddr::default().is_zero());
        assert!(!MAC.is_zero());
    }

    #[test]
    fn is_broadcast() {
        assert!(MacAddr::new(0xff, 0xff, 0xff, 0xff, 0xff, 0xff).is_broadcast());
        assert!(!MAC.is_broadcast());
    }

    #[test]
    fn ordering() {
        let a = MacAddr::new(0x00, 0x00, 0x00, 0x00, 0x00, 0x01);
        let b = MacAddr::new(0x00, 0x00, 0x00, 0x00, 0x00, 0x02);
        assert!(a < b);
    }
}
