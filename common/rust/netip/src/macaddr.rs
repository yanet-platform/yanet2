//! MAC (EUI-48) address type.
//!
//! This module provides [`MacAddr`], a lightweight wrapper around a 48-bit
//! Ethernet hardware address.
//!
//! # Examples
//!
//! ```
//! use netip::MacAddr;
//!
//! let mac = MacAddr::new(0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9);
//!
//! let parsed = "3a:ac:26:9b:5b:f9".parse().unwrap();
//! assert_eq!(mac, parsed);
//!
//! assert_eq!("3a:ac:26:9b:5b:f9", mac.to_string());
//! ```

use core::{
    fmt::{self, Display, Formatter},
    str::FromStr,
};

/// A 48-bit MAC (EUI-48) address.
///
/// Internally stored as the lower 48 bits of a [`u64`] for efficient
/// comparison and hashing. The upper 16 bits are always zero.
///
/// # Parsing
///
/// Three formats are supported via [`FromStr`] and [`MacAddr::parse_ascii`]:
///
/// | Format           | Example             |
/// |------------------|---------------------|
/// | Colon-separated  | `aa:bb:cc:dd:ee:ff` |
/// | Hyphen-separated | `aa-bb-cc-dd-ee-ff` |
///
/// Hex digits are case-insensitive.
///
/// # Display
///
/// The [`Display`] implementation always uses lowercase colon-separated
/// format: `aa:bb:cc:dd:ee:ff`.
///
/// # Examples
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
#[derive(Debug, Clone, Copy, PartialEq, Eq, Ord, PartialOrd, Hash)]
pub struct MacAddr(u64);

impl MacAddr {
    /// The zero address (`00:00:00:00:00:00`).
    pub const ZERO: MacAddr = MacAddr::new(0, 0, 0, 0, 0, 0);

    /// The broadcast address (`ff:ff:ff:ff:ff:ff`).
    ///
    /// Frames sent to this address are received by all stations on the
    /// local network segment.
    pub const BROADCAST: MacAddr = MacAddr::new(0xff, 0xff, 0xff, 0xff, 0xff, 0xff);

    /// Creates a new MAC address from its 6 octets in transmission order.
    ///
    /// Each parameter represents one byte of the MAC address in the standard
    /// format `xx:xx:xx:xx:xx:xx`.
    #[inline]
    pub const fn new(a: u8, b: u8, c: u8, d: u8, e: u8, f: u8) -> MacAddr {
        Self(u64::from_be_bytes([0, 0, a, b, c, d, e, f]))
    }

    /// Parses a MAC address from an ASCII byte slice.
    ///
    /// | Format           | Example             |
    /// |------------------|---------------------|
    /// | Colon-separated  | `xx:xx:xx:xx:xx:xx` |
    /// | Hyphen-separated | `xx-xx-xx-xx-xx-xx` |
    ///
    /// ```
    /// use netip::MacAddr;
    ///
    /// let mac = MacAddr::parse_ascii(b"3a:ac:26:9b:5b:f9").unwrap();
    ///
    /// assert_eq!(mac, MacAddr::parse_ascii(b"3a-ac-26-9b-5b-f9").unwrap());
    /// ```
    pub fn parse_ascii(b: &[u8]) -> Result<Self, MacAddrParseError> {
        match b.len() {
            17 => parse_separated(b),
            _ => Err(MacAddrParseError),
        }
    }

    /// Returns the numeric value of this address as a [`u64`].
    ///
    /// The address occupies the lower 48 bits; the upper 16 bits are zero.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::MacAddr;
    ///
    /// assert_eq!(0, MacAddr::ZERO.as_u64());
    /// assert_eq!(1, MacAddr::new(0, 0, 0, 0, 0, 1).as_u64());
    /// ```
    #[inline]
    pub const fn as_u64(&self) -> u64 {
        match self {
            Self(addr) => *addr,
        }
    }

    /// Returns the 6 octets in network (transmission) order.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::MacAddr;
    ///
    /// let mac = MacAddr::new(0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9);
    /// assert_eq!([0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9], mac.octets());
    /// ```
    #[inline]
    pub const fn octets(&self) -> [u8; 6] {
        let [.., a, b, c, d, e, f] = self.as_u64().to_be_bytes();
        [a, b, c, d, e, f]
    }

    /// Returns `true` if all octets are zero (`00:00:00:00:00:00`).
    #[inline]
    pub const fn is_zero(&self) -> bool {
        self.as_u64() == 0
    }

    /// Returns `true` if this is the broadcast address (`ff:ff:ff:ff:ff:ff`).
    ///
    /// Broadcast frames are delivered to every station on the local segment.
    #[inline]
    pub const fn is_broadcast(&self) -> bool {
        self.as_u64() == 0xffff_ffff_ffff
    }

    /// Returns `true` if this is a multicast address.
    ///
    /// A MAC address is multicast when the least-significant bit of the
    /// first octet (the I/G bit) is set.
    ///
    /// Note that the broadcast address is also multicast.
    ///
    /// ```
    /// use netip::MacAddr;
    ///
    /// assert!(MacAddr::new(0x01, 0x00, 0x5e, 0x00, 0x00, 0x01).is_multicast());
    /// assert!(!MacAddr::new(0x00, 0x1a, 0x2b, 0x3c, 0x4d, 0x5e).is_multicast());
    /// ```
    #[inline]
    pub const fn is_multicast(&self) -> bool {
        (self.as_u64() >> 40) & 0x01 != 0
    }

    /// Returns `true` if this is a unicast address.
    ///
    /// The complement of [`is_multicast`](MacAddr::is_multicast): the I/G bit
    /// in the first octet is clear.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::MacAddr;
    ///
    /// assert!(MacAddr::new(0x00, 0x1a, 0x2b, 0x3c, 0x4d, 0x5e).is_unicast());
    /// assert!(!MacAddr::BROADCAST.is_unicast());
    /// ```
    #[inline]
    pub const fn is_unicast(&self) -> bool {
        !self.is_multicast()
    }

    /// Returns `true` if this is a locally administered address.
    ///
    /// The U/L bit (second-least-significant bit of the first octet) is set
    /// for addresses that are **not** assigned by an IEEE-registered
    /// manufacturer.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::MacAddr;
    ///
    /// // Bit 0x02 set -> locally administered.
    /// assert!(MacAddr::new(0x02, 0x00, 0x00, 0x00, 0x00, 0x01).is_locally_administered());
    ///
    /// // Bit 0x02 clear -> universally administered.
    /// assert!(!MacAddr::new(0x00, 0x1a, 0x2b, 0x3c, 0x4d, 0x5e).is_locally_administered());
    /// ```
    #[inline]
    pub const fn is_locally_administered(&self) -> bool {
        (self.as_u64() >> 40) & 0x02 != 0
    }

    /// Returns `true` if this is a universally administered address.
    ///
    /// The complement of
    /// [`is_locally_administered`](MacAddr::is_locally_administered).
    #[inline]
    pub const fn is_universally_administered(&self) -> bool {
        !self.is_locally_administered()
    }
}

/// The default MAC address is [`MacAddr::ZERO`].
impl Default for MacAddr {
    #[inline]
    fn default() -> Self {
        Self::ZERO
    }
}

/// Creates a MAC address from a [`u64`].
///
/// Only the lower 48 bits are used; the upper 16 bits are silently masked.
impl From<u64> for MacAddr {
    #[inline]
    fn from(addr: u64) -> MacAddr {
        MacAddr(addr & 0xffff_ffff_ffff)
    }
}

impl From<MacAddr> for u64 {
    #[inline]
    fn from(mac: MacAddr) -> u64 {
        mac.as_u64()
    }
}

impl From<[u8; 6]> for MacAddr {
    #[inline]
    fn from(octets: [u8; 6]) -> MacAddr {
        MacAddr::new(octets[0], octets[1], octets[2], octets[3], octets[4], octets[5])
    }
}

impl From<MacAddr> for [u8; 6] {
    #[inline]
    fn from(mac: MacAddr) -> [u8; 6] {
        mac.octets()
    }
}

impl Display for MacAddr {
    fn fmt(&self, fmt: &mut Formatter) -> Result<(), fmt::Error> {
        let [a, b, c, d, e, f] = self.octets();

        write!(fmt, "{a:02x}:{b:02x}:{c:02x}:{d:02x}:{e:02x}:{f:02x}")
    }
}

impl FromStr for MacAddr {
    type Err = MacAddrParseError;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Self::parse_ascii(s.as_bytes())
    }
}

/// Decodes a single ASCII hex digit.
const fn decode_hex(b: u8) -> Result<u8, MacAddrParseError> {
    match b {
        b'0'..=b'9' => Ok(b - b'0'),
        b'a'..=b'f' => Ok(b - b'a' + 10),
        b'A'..=b'F' => Ok(b - b'A' + 10),
        _ => Err(MacAddrParseError),
    }
}

/// Decodes two ASCII hex digits into a single byte.
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
///
/// The separator character is determined from byte 2 and must be consistent
/// across all positions. Mixed separators (e.g. `aa:bb-cc:dd-ee:ff`) are
/// rejected.
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

/// Error returned when parsing a MAC address string fails.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
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
    const MAC_U64: u64 = 0x3aac269b5bf9;

    #[test]
    fn as_u64() {
        assert_eq!(MAC_U64, MAC.as_u64());
    }

    #[test]
    fn from_u64_masks_high_bits() {
        let mac = MacAddr::from(0xabcd_3aac269b5bf9);

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
        assert!(MacAddr::ZERO.is_zero());
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
        let c = MacAddr::new(0x01, 0x00, 0x00, 0x00, 0x00, 0x00);
        assert!(a < b);
        assert!(b < c);
    }
}
