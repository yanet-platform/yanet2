//! IPv4/IPv6 network primitives.
//!
//! This module provides types for IP networks by extending the standard
//! library.
//!
//! Unlike most (more precisely, all of them) open-source libraries, this module
//! is designed to support non-contiguous masks.

use core::{
    convert::TryFrom,
    error::Error,
    fmt::{Debug, Display, Formatter},
    net::{AddrParseError, IpAddr, Ipv4Addr, Ipv6Addr},
    num::ParseIntError,
    ops::{Deref, Range},
    str::FromStr,
};

const IPV4_ALL_BITS: Ipv4Addr = Ipv4Addr::new(0xff, 0xff, 0xff, 0xff);
const IPV6_ALL_BITS: Ipv6Addr = Ipv6Addr::new(0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff);

/// An error that is returned during IP network conversion from address and CIDR
/// when the CIDR is too large.
#[derive(Debug, PartialEq, Eq)]
pub struct CidrOverflowError(u8, u8);

impl Display for CidrOverflowError {
    fn fmt(&self, fmt: &mut Formatter) -> Result<(), core::fmt::Error> {
        let Self(from, to) = self;
        write!(fmt, "invalid CIDR: {from} must be <= {to}")
    }
}

/// An error that is returned during IP network parsing.
#[derive(Debug, PartialEq)]
pub enum IpNetParseError {
    /// Expected IP network, but got something malformed instead.
    ExpectedIpNetwork,
    /// Expected IP address.
    ExpectedIpAddr,
    /// Failed to parse IP address.
    AddrParseError(AddrParseError),
    /// Expected CIDR number after `/`.
    ExpectedIpCidr,
    /// Failed to parse CIDR mask.
    CidrParseError(ParseIntError),
    /// CIDR mask is too large.
    CidrOverflow(CidrOverflowError),
}

impl Display for IpNetParseError {
    fn fmt(&self, fmt: &mut Formatter) -> Result<(), core::fmt::Error> {
        match self {
            Self::ExpectedIpNetwork => {
                write!(fmt, "expected IP network")
            }
            Self::ExpectedIpAddr => {
                write!(fmt, "expected IP address")
            }
            Self::AddrParseError(err) => {
                write!(fmt, "IP address parse error: {err}")
            }
            Self::ExpectedIpCidr => {
                write!(fmt, "expected IP CIDR")
            }
            Self::CidrParseError(err) => {
                write!(fmt, "CIDR parse error: {err}")
            }
            Self::CidrOverflow(err) => {
                write!(fmt, "CIDR overflow: {err}")
            }
        }
    }
}

impl Error for IpNetParseError {}

impl From<AddrParseError> for IpNetParseError {
    #[inline]
    fn from(err: AddrParseError) -> Self {
        Self::AddrParseError(err)
    }
}

impl From<CidrOverflowError> for IpNetParseError {
    #[inline]
    fn from(err: CidrOverflowError) -> Self {
        Self::CidrOverflow(err)
    }
}

/// Returns an [`Ipv4Addr`] that will have exact `cidr` leading bits set to
/// one.
///
/// This function is used to construct IP masks for the given network prefix
/// size.
#[inline]
pub fn ipv4_mask_from_cidr(cidr: u8) -> Result<Ipv4Addr, CidrOverflowError> {
    if cidr <= 32 {
        let mask = !(u32::MAX.checked_shr(cidr as u32).unwrap_or_default());
        Ok(mask.into())
    } else {
        Err(CidrOverflowError(cidr, 32))
    }
}

/// Returns an [`Ipv6Addr`] that will have exact `cidr` leading bits set to
/// one.
///
/// This function is used to construct IP masks for the given network prefix
/// size.
#[inline]
pub fn ipv6_mask_from_cidr(cidr: u8) -> Result<Ipv6Addr, CidrOverflowError> {
    if cidr <= 128 {
        let mask = !(u128::MAX.checked_shr(cidr as u32).unwrap_or_default());
        Ok(mask.into())
    } else {
        Err(CidrOverflowError(cidr, 128))
    }
}

/// An IP network, either IPv4 or IPv6.
///
/// This enum can contain either an [`Ipv4Network`] or an [`Ipv6Network`], see
/// their respective documentation for more details.
#[derive(Debug, Copy, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum IpNetwork {
    /// An IPv4 network.
    V4(Ipv4Network),
    /// An IPv6 network.
    V6(Ipv6Network),
}

impl IpNetwork {
    /// Parses specified buffer into an extended IP network.
    ///
    /// The following formats are supported:
    ///
    /// | Format                   | Example                           |
    /// |--------------------------|-----------------------------------|
    /// | IPv4                     | `77.88.55.242`                    |
    /// | IPv4/CIDR                | `77.88.0.0/16`                    |
    /// | IPv4/IPv4                | `77.88.0.0/255.255.0.0`           |
    /// | IPv6                     | `2a02:6b8::2:242`                 |
    /// | IPv6/CIDR                | `2a02:6b8:c00::/40`               |
    /// | IPv6/IPv6                | `2a02:6b8:c00::/ffff:ffff:ff00::` |
    ///
    /// # Note
    ///
    /// During construction, the address is normalized using mask, for example
    /// the network `77.88.55.242/16` becomes `77.88.0.0/16`.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::{Ipv4Addr, Ipv6Addr};
    ///
    /// use netip::{IpNetwork, Ipv4Network, Ipv6Network};
    ///
    /// // Here are some examples to show supported formats and the output of this function.
    ///
    /// // We can parse IPv4 addresses without explicit `/32` mask.
    /// assert_eq!(
    ///     IpNetwork::V4(Ipv4Network::new(
    ///         Ipv4Addr::new(77, 88, 55, 242),
    ///         Ipv4Addr::new(255, 255, 255, 255),
    ///     ),),
    ///     // IPv4 address.
    ///     IpNetwork::parse("77.88.55.242").unwrap(),
    /// );
    ///
    /// // When a mask can be represented in common CIDR format, like `/16`,
    /// // which means "leading 16 bits on the mask is set to one".
    /// assert_eq!(
    ///     IpNetwork::V4(Ipv4Network::new(
    ///         Ipv4Addr::new(77, 88, 0, 0),
    ///         Ipv4Addr::new(255, 255, 0, 0),
    ///     ),),
    ///     // IPv4 CIDR network.
    ///     IpNetwork::parse("77.88.0.0/16").unwrap(),
    /// );
    ///
    /// // But we can also specify mask explicitly. This is useful when we
    /// // want non-contiguous mask.
    /// assert_eq!(
    ///     IpNetwork::V4(Ipv4Network::new(
    ///         Ipv4Addr::new(77, 88, 0, 0),
    ///         Ipv4Addr::new(255, 255, 0, 0),
    ///     ),),
    ///     // IPv4 network with explicit mask.
    ///     IpNetwork::parse("77.88.0.0/255.255.0.0").unwrap(),
    /// );
    ///
    /// // Parse IPv6 addresses without explicit `/128` mask.
    /// assert_eq!(
    ///     IpNetwork::V6(Ipv6Network::new(
    ///         Ipv6Addr::new(0x2a02, 0x6b8, 0, 0, 0, 0, 0x2, 0x242),
    ///         Ipv6Addr::new(
    ///             0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff
    ///         ),
    ///     ),),
    ///     // IPv6 address.
    ///     IpNetwork::parse("2a02:6b8::2:242").unwrap(),
    /// );
    ///
    /// // CIDR format.
    /// assert_eq!(
    ///     IpNetwork::V6(Ipv6Network::new(
    ///         Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0, 0, 0),
    ///         Ipv6Addr::new(0xffff, 0xffff, 0xff00, 0, 0, 0, 0, 0),
    ///     ),),
    ///     // IPv6 CIDR network.
    ///     IpNetwork::parse("2a02:6b8:c00::/40").unwrap(),
    /// );
    ///
    /// // Network with explicit mask (contiguous in this case).
    /// assert_eq!(
    ///     IpNetwork::V6(Ipv6Network::new(
    ///         Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0, 0, 0),
    ///         Ipv6Addr::new(0xffff, 0xffff, 0xff00, 0, 0, 0, 0, 0),
    ///     ),),
    ///     // IPv6 network with explicit mask.
    ///     IpNetwork::parse("2a02:6b8:c00::/ffff:ffff:ff00::").unwrap(),
    /// );
    ///
    /// // Network with explicit non-contiguous mask.
    /// assert_eq!(
    ///     IpNetwork::V6(Ipv6Network::new(
    ///         Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0x1234, 0, 0),
    ///         Ipv6Addr::new(0xffff, 0xffff, 0xff00, 0, 0xffff, 0xffff, 0, 0),
    ///     ),),
    ///     // The same network as above, but specified explicitly.
    ///     IpNetwork::parse("2a02:6b8:c00::1234:0:0/ffff:ffff:ff00::ffff:ffff:0:0").unwrap(),
    /// );
    /// ```
    pub fn parse(buf: &str) -> Result<Self, IpNetParseError> {
        match Ipv4Network::parse(buf) {
            Ok(net) => {
                return Ok(Self::V4(net));
            }
            Err(IpNetParseError::AddrParseError(..)) => {}
            Err(err) => {
                return Err(err);
            }
        }

        let net = Ipv6Network::parse(buf)?;
        Ok(Self::V6(net))
    }

    /// Checks whether this network is a contiguous, i.e. contains mask with
    /// only leading bits set to one contiguously.
    #[inline]
    pub const fn is_contiguous(&self) -> bool {
        match self {
            Self::V4(net) => net.is_contiguous(),
            Self::V6(net) => net.is_contiguous(),
        }
    }

    /// Returns the mask prefix, i.e. the number of leading bits set, if this
    /// network is a contiguous one, `None` otherwise.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::IpNetwork;
    ///
    /// assert_eq!(
    ///     Some(24),
    ///     IpNetwork::parse("192.168.1.0/24").unwrap().prefix()
    /// );
    ///
    /// assert_eq!(
    ///     Some(40),
    ///     IpNetwork::parse("2a02:6b8::/40").unwrap().prefix()
    /// );
    /// assert_eq!(Some(128), IpNetwork::parse("2a02:6b8::1").unwrap().prefix());
    /// ```
    #[inline]
    pub const fn prefix(&self) -> Option<u8> {
        match self {
            Self::V4(net) => net.prefix(),
            Self::V6(net) => net.prefix(),
        }
    }
}

impl Display for IpNetwork {
    fn fmt(&self, fmt: &mut Formatter) -> Result<(), core::fmt::Error> {
        match self {
            IpNetwork::V4(net) => Display::fmt(&net, fmt),
            IpNetwork::V6(net) => Display::fmt(&net, fmt),
        }
    }
}

impl From<Ipv4Addr> for IpNetwork {
    #[inline]
    fn from(addr: Ipv4Addr) -> Self {
        // Construct network directly, because no address mutation occurs.
        Self::V4(Ipv4Network(addr, IPV4_ALL_BITS))
    }
}

impl TryFrom<(Ipv4Addr, u8)> for IpNetwork {
    type Error = CidrOverflowError;

    fn try_from((addr, cidr): (Ipv4Addr, u8)) -> Result<Self, Self::Error> {
        Ok(Self::V4(Ipv4Network::new(addr, ipv4_mask_from_cidr(cidr)?)))
    }
}

impl From<(Ipv4Addr, Ipv4Addr)> for IpNetwork {
    fn from((addr, mask): (Ipv4Addr, Ipv4Addr)) -> Self {
        Self::V4(Ipv4Network::new(addr, mask))
    }
}

impl From<Ipv4Network> for IpNetwork {
    #[inline]
    fn from(net: Ipv4Network) -> Self {
        Self::V4(net)
    }
}

impl From<Ipv6Addr> for IpNetwork {
    fn from(addr: Ipv6Addr) -> Self {
        // Construct network directly, because no address mutation occurs.
        Self::V6(Ipv6Network(addr, IPV6_ALL_BITS))
    }
}

impl TryFrom<(Ipv6Addr, u8)> for IpNetwork {
    type Error = CidrOverflowError;

    fn try_from((addr, cidr): (Ipv6Addr, u8)) -> Result<Self, Self::Error> {
        Ok(Self::V6(Ipv6Network::new(addr, ipv6_mask_from_cidr(cidr)?)))
    }
}

impl From<(Ipv6Addr, Ipv6Addr)> for IpNetwork {
    fn from((addr, mask): (Ipv6Addr, Ipv6Addr)) -> Self {
        Self::V6(Ipv6Network::new(addr, mask))
    }
}

impl From<Ipv6Network> for IpNetwork {
    #[inline]
    fn from(net: Ipv6Network) -> Self {
        Self::V6(net)
    }
}

impl From<IpAddr> for IpNetwork {
    #[inline]
    fn from(addr: IpAddr) -> Self {
        match addr {
            IpAddr::V4(addr) => Self::from(addr),
            IpAddr::V6(addr) => Self::from(addr),
        }
    }
}

impl FromStr for IpNetwork {
    type Err = IpNetParseError;

    #[inline]
    fn from_str(s: &str) -> Result<Self, Self::Err> {
        IpNetwork::parse(s)
    }
}

/// Represents the network range in which IP addresses are IPv4.
///
/// IPv4 networks are defined as pair of two [`Ipv4Addr`]. One for IP address
/// and the second one - for mask.
///
/// See [`IpNetwork`] for a type encompassing both IPv4 and IPv6 networks.
///
/// # Note
///
/// An IPv4 network represents a range of IPv4 addresses. The most common way to
/// represent an IP network is through CIDR notation. See [IETF RFC 4632] for
/// CIDR notation.
///
/// An example of such notation is `77.152.124.0/24`.
///
/// In CIDR notation, a prefix is shown as a 4-octet quantity, just like a
/// traditional IPv4 address or network number, followed by the "/" (slash)
/// character, followed by a decimal value between 0 and 32 that describes the
/// number of significant bits.
///
/// Here, the "/24" indicates that the mask to extract the network portion of
/// the prefix is a 32-bit value where the most significant 24 bits are
/// ones and the least significant 8 bits are zeros.
///
/// The other way to represent the network above is to use explicit notation,
/// such as `77.152.124.0/255.255.255.0`.
///
/// However, this struct supports non-contiguous masks, i.e. masks with
/// non-contiguous bits set to one, like `77.152.0.55/255.255.0.255`.
///
/// [IETF RFC 4632]: https://tools.ietf.org/html/rfc4632
#[derive(Debug, Copy, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Ipv4Network(Ipv4Addr, Ipv4Addr);

impl Ipv4Network {
    /// An IPv4 network representing the unspecified network: `0.0.0.0/0`
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv4Addr;
    ///
    /// use netip::Ipv4Network;
    ///
    /// let net = Ipv4Network::UNSPECIFIED;
    /// assert_eq!(
    ///     Ipv4Network::new(Ipv4Addr::new(0, 0, 0, 0), Ipv4Addr::new(0, 0, 0, 0)),
    ///     net
    /// );
    /// ```
    pub const UNSPECIFIED: Self = Self(Ipv4Addr::UNSPECIFIED, Ipv4Addr::UNSPECIFIED);

    /// Initializes a new [`Ipv4Network`] using specified address and mask.
    ///
    /// During construction the address is normalized using mask, i.e. in case
    /// of `192.168.1.1/255.255.255.0` the result will be transformed
    /// to `192.168.1.0/255.255.255.0`.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv4Addr;
    ///
    /// use netip::Ipv4Network;
    ///
    /// let net = Ipv4Network::new(
    ///     Ipv4Addr::new(192, 168, 1, 1),
    ///     Ipv4Addr::new(255, 255, 255, 0),
    /// );
    ///
    /// assert_eq!(Ipv4Addr::new(192, 168, 1, 0), *net.addr());
    /// assert_eq!(Ipv4Addr::new(255, 255, 255, 0), *net.mask());
    /// ```
    #[inline]
    pub const fn new(addr: Ipv4Addr, mask: Ipv4Addr) -> Self {
        let addr = u32::from_be_bytes(addr.octets()) & u32::from_be_bytes(mask.octets());

        Self(Ipv4Addr::from_bits(addr), mask)
    }

    /// Parses the specified buffer into an IPv4 network.
    ///
    /// The following formats are supported:
    /// - IPv4
    /// - IPv4/CIDR
    /// - IPv4/IPv4
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv4Addr;
    ///
    /// use netip::Ipv4Network;
    ///
    /// let expected = Ipv4Network::new(
    ///     Ipv4Addr::new(192, 168, 1, 1),
    ///     Ipv4Addr::new(255, 255, 255, 255),
    /// );
    ///
    /// assert_eq!(expected, Ipv4Network::parse("192.168.1.1").unwrap());
    /// assert_eq!(expected, Ipv4Network::parse("192.168.1.1/32").unwrap());
    /// assert_eq!(
    ///     expected,
    ///     Ipv4Network::parse("192.168.1.1/255.255.255.255").unwrap()
    /// );
    /// ```
    pub fn parse(buf: &str) -> Result<Self, IpNetParseError> {
        let mut parts = buf.splitn(2, '/');
        let addr = parts.next().ok_or(IpNetParseError::ExpectedIpAddr)?;
        let addr: Ipv4Addr = addr.parse()?;
        match parts.next() {
            Some(mask) => match mask.parse::<u8>() {
                Ok(cidr) => Ok(Self::try_from((addr, cidr))?),
                Err(..) => {
                    // Maybe explicit mask.
                    let mask: Ipv4Addr = mask.parse()?;
                    Ok(Self::new(addr, mask))
                }
            },
            None => Ok(Self(addr, IPV4_ALL_BITS)),
        }
    }

    /// Returns the IP address of this IPv4 network.
    #[inline]
    pub const fn addr(&self) -> &Ipv4Addr {
        match self {
            Self(addr, ..) => addr,
        }
    }

    /// Returns the IP mask of this IPv4 network.
    #[inline]
    pub const fn mask(&self) -> &Ipv4Addr {
        match self {
            Self(.., mask) => mask,
        }
    }

    /// Converts a pair of IPv4 address and mask represented as a pair of host
    /// byte order `u32` into an IPv4 network.
    ///
    /// # Note
    ///
    /// During conversion, the address is normalized using mask.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv4Addr;
    ///
    /// use netip::Ipv4Network;
    ///
    /// let expected = Ipv4Network::new(
    ///     Ipv4Addr::new(192, 168, 1, 0),
    ///     Ipv4Addr::new(255, 255, 255, 0),
    /// );
    ///
    /// assert_eq!(expected, Ipv4Network::from_bits(3232235776, 4294967040));
    /// ```
    #[inline]
    pub const fn from_bits(addr: u32, mask: u32) -> Self {
        Self::new(Ipv4Addr::from_bits(addr), Ipv4Addr::from_bits(mask))
    }

    /// Converts this IPv4 network's address and mask into pair of host byte
    /// order `u32`.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv4Addr;
    ///
    /// use netip::Ipv4Network;
    ///
    /// let net = Ipv4Network::new(
    ///     Ipv4Addr::new(192, 168, 1, 0),
    ///     Ipv4Addr::new(255, 255, 255, 0),
    /// );
    /// assert_eq!((3232235776, 4294967040), net.to_bits());
    /// ```
    #[inline]
    pub const fn to_bits(&self) -> (u32, u32) {
        match self {
            Self(addr, mask) => (u32::from_be_bytes(addr.octets()), u32::from_be_bytes(mask.octets())),
        }
    }

    /// Returns `true` if this IPv4 network fully contains the specified one.
    #[inline]
    pub const fn contains(&self, other: &Ipv4Network) -> bool {
        let (a1, m1) = self.to_bits();
        let (a2, m2) = other.to_bits();

        (a2 & m1 == a1) && (m2 & m1 == m1)
    }

    /// Checks whether this network is a contiguous, i.e. contains mask with
    /// only leading bits set to one contiguously.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::Ipv4Network;
    ///
    /// assert!(Ipv4Network::parse("0.0.0.0/0").unwrap().is_contiguous());
    /// assert!(
    ///     Ipv4Network::parse("213.180.192.0/19")
    ///         .unwrap()
    ///         .is_contiguous()
    /// );
    ///
    /// assert!(
    ///     !Ipv4Network::parse("213.180.0.192/255.255.0.255")
    ///         .unwrap()
    ///         .is_contiguous()
    /// );
    /// ```
    #[inline]
    pub const fn is_contiguous(&self) -> bool {
        let mask = u32::from_be_bytes(self.mask().octets());
        mask.count_zeros() == mask.trailing_zeros()
    }

    /// Returns the mask prefix, i.e. the number of leading bits set, if this
    /// network is a contiguous one, `None` otherwise.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv4Addr;
    ///
    /// use netip::Ipv4Network;
    ///
    /// assert_eq!(
    ///     Some(24),
    ///     Ipv4Network::new(
    ///         Ipv4Addr::new(192, 168, 1, 0),
    ///         Ipv4Addr::new(255, 255, 255, 0)
    ///     )
    ///     .prefix()
    /// );
    /// assert_eq!(
    ///     Some(32),
    ///     Ipv4Network::new(
    ///         Ipv4Addr::new(192, 168, 1, 1),
    ///         Ipv4Addr::new(255, 255, 255, 255)
    ///     )
    ///     .prefix()
    /// );
    ///
    /// assert_eq!(
    ///     None,
    ///     Ipv4Network::new(Ipv4Addr::new(192, 0, 1, 0), Ipv4Addr::new(255, 0, 255, 0)).prefix()
    /// );
    /// ```
    #[inline]
    pub const fn prefix(&self) -> Option<u8> {
        let mask = u32::from_be_bytes(self.mask().octets());
        let ones = mask.leading_ones();

        if mask.count_ones() == ones {
            Some(ones as u8)
        } else {
            None
        }
    }

    /// Converts this network to a contiguous network by clearing mask bits set
    /// to one after the first zero bit.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv4Addr;
    ///
    /// use netip::Ipv4Network;
    ///
    /// let net = Ipv4Network::new(
    ///     Ipv4Addr::new(192, 168, 0, 1),
    ///     Ipv4Addr::new(255, 255, 0, 255),
    /// );
    /// let expected = Ipv4Network::new(Ipv4Addr::new(192, 168, 0, 0), Ipv4Addr::new(255, 255, 0, 0));
    ///
    /// assert_eq!(expected, net.to_contiguous());
    /// ```
    pub fn to_contiguous(&self) -> Self {
        let (addr, mask) = self.to_bits();

        Self::try_from((addr.into(), mask.leading_ones() as u8)).expect("CIDR must be in range [0; 32]")
    }

    /// Calculates and returns the [`Ipv4Network`], which will be a supernet,
    /// i.e. the shortest common network both for this network and the
    /// specified ones.
    ///
    /// # Note
    ///
    /// This function works fine for both contiguous and non-contiguous
    /// networks.
    ///
    /// Use [`Ipv4Network::to_contiguous`] method if you need to convert the
    /// output network.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv4Addr;
    ///
    /// use netip::Ipv4Network;
    ///
    /// let net0 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 0), 25)).unwrap();
    /// let net1 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 128), 25)).unwrap();
    ///
    /// assert_eq!(
    ///     Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 0), 24)).unwrap(),
    ///     net0.supernet_for(&[net1])
    /// );
    /// ```
    pub fn supernet_for(&self, nets: &[Ipv4Network]) -> Ipv4Network {
        let (addr, mask) = self.to_bits();

        let mut min = addr;
        let mut max = addr;

        for net in nets {
            let (addr, ..) = net.to_bits();
            if addr < min {
                min = addr;
            } else if addr > max {
                max = addr;
            }
        }

        let common_addr = min ^ max;
        let common_addr_len = common_addr.leading_zeros();
        let mask = mask & !(u32::MAX.checked_shr(common_addr_len).unwrap_or_default());

        Self::new(addr.into(), mask.into())
    }

    /// Converts this network to an IPv4-mapped IPv6 network.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::{Ipv4Addr, Ipv6Addr};
    ///
    /// use netip::{Ipv4Network, Ipv6Network};
    ///
    /// let net = Ipv4Network::new(
    ///     Ipv4Addr::new(192, 168, 1, 42),
    ///     Ipv4Addr::new(255, 255, 252, 0),
    /// );
    /// let expected =
    ///     Ipv6Network::parse("::ffff:c0a8:0/ffff:ffff:ffff:ffff:ffff:ffff:ffff:fc00").unwrap();
    /// assert_eq!(expected, net.to_ipv6_mapped());
    /// ```
    #[inline]
    pub const fn to_ipv6_mapped(&self) -> Ipv6Network {
        match self {
            Ipv4Network(addr, mask) => {
                let [a, b, c, d] = mask.octets();
                let [a, b, c, d]: [u16; 4] = [a as u16, b as u16, c as u16, d as u16];
                Ipv6Network(
                    addr.to_ipv6_mapped(),
                    Ipv6Addr::new(0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, a << 8 | b, c << 8 | d),
                )
            }
        }
    }
}

impl Display for Ipv4Network {
    /// Formats this IPv4 network using the given formatter.
    ///
    /// The idea is to format using **the most compact** representation, except
    /// for non-contiguous networks, which are formatted explicitly.
    fn fmt(&self, fmt: &mut Formatter) -> Result<(), core::fmt::Error> {
        match self {
            Self(addr, mask) => {
                match self.prefix() {
                    Some(prefix) => fmt.write_fmt(format_args!("{}/{}", addr, prefix)),
                    None => {
                        // This is a non-contiguous network, expand the mask.
                        fmt.write_fmt(format_args!("{}/{}", addr, mask))
                    }
                }
            }
        }
    }
}

impl From<Ipv4Addr> for Ipv4Network {
    fn from(addr: Ipv4Addr) -> Self {
        Self(addr, IPV4_ALL_BITS)
    }
}

impl TryFrom<(Ipv4Addr, u8)> for Ipv4Network {
    type Error = CidrOverflowError;

    fn try_from((addr, cidr): (Ipv4Addr, u8)) -> Result<Self, Self::Error> {
        let mask = ipv4_mask_from_cidr(cidr)?;
        Ok(Self::new(addr, mask))
    }
}

impl FromStr for Ipv4Network {
    type Err = IpNetParseError;

    #[inline]
    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Ipv4Network::parse(s)
    }
}

impl AsRef<Ipv4Network> for Ipv4Network {
    #[inline]
    fn as_ref(&self) -> &Ipv4Network {
        self
    }
}

/// Represents the network range in which IP addresses are IPv6.
///
/// IPv6 networks are defined as pair of two [`Ipv6Addr`]. One for IP address
/// and the second one - for mask.
///
/// See [`IpNetwork`] for a type encompassing both IPv4 and IPv6 networks.
///
/// # Note
///
/// An IPv6 network represents a range of IPv6 addresses. The most common way to
/// represent an IP network is through CIDR notation. See [IETF RFC 4632] for
/// CIDR notation.
///
/// An example of such notation is `2a02:6b8:c00::/40`.
///
/// In CIDR notation, a prefix is shown as a 8-hextet quantity, just like a
/// traditional IPv6 address or network number, followed by the "/" (slash)
/// character, followed by a decimal value between 0 and 128 that describes the
/// number of significant bits.
///
/// Here, the "/40" indicates that the mask to extract the network portion of
/// the prefix is a 128-bit value where the most significant 40 bits are
/// ones and the least significant 88 bits are zeros.
///
/// The other way to represent the network above is to use explicit notation,
/// such as `2a02:6b8:c00::/ffff:ffff:ff00::`.
///
/// However, this struct supports non-contiguous masks, i.e. masks with
/// non-contiguous bits set to one, like the following one:
/// `2a02:6b8:c00::1234:0:0/ffff:ffff:ff00::ffff:ffff:0:0`.
///
/// [IETF RFC 4632]: https://tools.ietf.org/html/rfc4632
#[derive(Debug, Copy, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Ipv6Network(Ipv6Addr, Ipv6Addr);

impl Ipv6Network {
    /// An IPv6 network representing the unspecified network: `::/0`
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv6Addr;
    ///
    /// use netip::Ipv6Network;
    ///
    /// let net = Ipv6Network::UNSPECIFIED;
    /// assert_eq!(
    ///     Ipv6Network::new(
    ///         Ipv6Addr::new(0, 0, 0, 0, 0, 0, 0, 0),
    ///         Ipv6Addr::new(0, 0, 0, 0, 0, 0, 0, 0)
    ///     ),
    ///     net
    /// );
    /// ```
    pub const UNSPECIFIED: Self = Self(Ipv6Addr::UNSPECIFIED, Ipv6Addr::UNSPECIFIED);

    /// Initializes a new [`Ipv6Network`] using specified address and mask.
    ///
    /// During construction, the address is normalized using mask.
    #[inline]
    pub const fn new(addr: Ipv6Addr, mask: Ipv6Addr) -> Self {
        let addr = u128::from_be_bytes(addr.octets()) & u128::from_be_bytes(mask.octets());

        Self(Ipv6Addr::from_bits(addr), mask)
    }

    /// Parses the specified buffer into an IPv6 network.
    ///
    /// The following formats are supported:
    ///
    /// | Format                   | Example                           |
    /// |--------------------------|-----------------------------------|
    /// | IPv6                     | `2a02:6b8::2:242`                 |
    /// | IPv6/CIDR                | `2a02:6b8:c00::/40`               |
    /// | IPv6/IPv6                | `2a02:6b8:c00::/ffff:ffff:ff00::` |
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv6Addr;
    ///
    /// use netip::Ipv6Network;
    ///
    /// // Here are some examples to show supported formats and the output of this function.
    ///
    /// // Parse IPv6 addresses without explicit `/128` mask.
    /// assert_eq!(
    ///     Ipv6Network::new(
    ///         Ipv6Addr::new(0x2a02, 0x6b8, 0, 0, 0, 0, 0x2, 0x242),
    ///         Ipv6Addr::new(
    ///             0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff
    ///         ),
    ///     ),
    ///     // IPv6 address.
    ///     Ipv6Network::parse("2a02:6b8::2:242").unwrap(),
    /// );
    ///
    /// // CIDR format.
    /// assert_eq!(
    ///     Ipv6Network::new(
    ///         Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0, 0, 0),
    ///         Ipv6Addr::new(0xffff, 0xffff, 0xff00, 0, 0, 0, 0, 0),
    ///     ),
    ///     // IPv6 CIDR network.
    ///     Ipv6Network::parse("2a02:6b8:c00::/40").unwrap(),
    /// );
    ///
    /// // Network with explicit mask (contiguous in this case).
    /// assert_eq!(
    ///     Ipv6Network::new(
    ///         Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0, 0, 0),
    ///         Ipv6Addr::new(0xffff, 0xffff, 0xff00, 0, 0, 0, 0, 0),
    ///     ),
    ///     // IPv6 network with explicit mask.
    ///     Ipv6Network::parse("2a02:6b8:c00::/ffff:ffff:ff00::").unwrap(),
    /// );
    ///
    /// // Network with explicit non-contiguous mask.
    /// assert_eq!(
    ///     Ipv6Network::new(
    ///         Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0x1234, 0, 0),
    ///         Ipv6Addr::new(0xffff, 0xffff, 0xff00, 0, 0xffff, 0xffff, 0, 0),
    ///     ),
    ///     // The same network as above, but specified explicitly.
    ///     Ipv6Network::parse("2a02:6b8:c00::1234:0:0/ffff:ffff:ff00::ffff:ffff:0:0").unwrap(),
    /// );
    /// ```
    pub fn parse(buf: &str) -> Result<Self, IpNetParseError> {
        let mut parts = buf.splitn(2, '/');
        let addr = parts.next().ok_or(IpNetParseError::ExpectedIpNetwork)?;
        let addr: Ipv6Addr = addr.parse()?;
        match parts.next() {
            Some(mask) => match mask.parse::<u8>() {
                Ok(mask) => Ok(Self::try_from((addr, mask))?),
                Err(..) => {
                    // Maybe explicit mask.
                    let mask: Ipv6Addr = mask.parse()?;
                    Ok(Self::new(addr, mask))
                }
            },
            None => Ok(Self(addr, IPV6_ALL_BITS)),
        }
    }

    /// Returns the IP address of this IPv6 network.
    #[inline]
    pub const fn addr(&self) -> &Ipv6Addr {
        match self {
            Ipv6Network(addr, ..) => addr,
        }
    }

    /// Returns the IP mask of this IPv6 network.
    #[inline]
    pub const fn mask(&self) -> &Ipv6Addr {
        match self {
            Ipv6Network(.., mask) => mask,
        }
    }

    /// Converts a pair of IPv6 address and mask represented as a pair of host
    /// byte order `u128` into an IPv6 network.
    ///
    /// # Note
    ///
    /// During conversion, the address is normalized using mask.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv6Addr;
    ///
    /// use netip::Ipv6Network;
    ///
    /// let addr = 0x2a0206b8b0817228000000000001000b_u128;
    /// let mask = 0xffffffffffffffffffffffffffffffff_u128;
    ///
    /// let expected = Ipv6Network::new(
    ///     Ipv6Addr::new(
    ///         0x2a02, 0x06b8, 0xb081, 0x7228, 0x0000, 0x0000, 0x0001, 0x000b,
    ///     ),
    ///     Ipv6Addr::new(
    ///         0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff,
    ///     ),
    /// );
    ///
    /// assert_eq!(expected, Ipv6Network::from_bits(addr, mask));
    /// ```
    #[inline]
    pub const fn from_bits(addr: u128, mask: u128) -> Self {
        Self::new(Ipv6Addr::from_bits(addr), Ipv6Addr::from_bits(mask))
    }

    /// Converts this IPv6 network's address and mask into pair of host byte
    /// order `u128`.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv6Addr;
    ///
    /// use netip::Ipv6Network;
    ///
    /// let net = Ipv6Network::new(
    ///     Ipv6Addr::new(
    ///         0x2a02, 0x06b8, 0xb081, 0x7228, 0x0000, 0x0000, 0x0001, 0x000b,
    ///     ),
    ///     Ipv6Addr::new(
    ///         0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff,
    ///     ),
    /// );
    /// assert_eq!(
    ///     (
    ///         0x2a0206b8b0817228000000000001000b_u128,
    ///         0xffffffffffffffffffffffffffffffff_u128,
    ///     ),
    ///     net.to_bits(),
    /// );
    /// ```
    #[inline]
    pub const fn to_bits(&self) -> (u128, u128) {
        match self {
            Self(addr, mask) => (u128::from_be_bytes(addr.octets()), u128::from_be_bytes(mask.octets())),
        }
    }

    /// Returns `true` if this IPv6 network fully contains the specified one.
    #[inline]
    pub const fn contains(&self, other: &Ipv6Network) -> bool {
        let (a1, m1) = self.to_bits();
        let (a2, m2) = other.to_bits();

        (a2 & m1 == a1) && (m2 & m1 == m1)
    }

    /// Checks whether this network is a contiguous, i.e. contains mask with
    /// only leading bits set to one contiguously.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::Ipv6Network;
    ///
    /// assert!(Ipv6Network::parse("::/0").unwrap().is_contiguous());
    /// assert!(
    ///     Ipv6Network::parse("2a02:6b8:c00::/40")
    ///         .unwrap()
    ///         .is_contiguous()
    /// );
    ///
    /// assert!(
    ///     !Ipv6Network::parse("2a02:6b8:c00::f800:0:0/ffff:ffff:0:0:ffff:ffff::")
    ///         .unwrap()
    ///         .is_contiguous()
    /// );
    /// ```
    #[inline]
    pub const fn is_contiguous(&self) -> bool {
        let mask = u128::from_be_bytes(self.mask().octets());
        mask.count_zeros() == mask.trailing_zeros()
    }

    /// Checks whether this network is an IPv4-mapped IPv6 network.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::{Ipv4Network, Ipv6Network};
    ///
    /// assert!(
    ///     Ipv4Network::parse("8.8.8.0/24")
    ///         .unwrap()
    ///         .to_ipv6_mapped()
    ///         .is_ipv4_mapped_ipv6()
    /// );
    ///
    /// assert!(
    ///     !Ipv6Network::parse("2a02:6b8::/40")
    ///         .unwrap()
    ///         .is_ipv4_mapped_ipv6()
    /// );
    /// ```
    #[inline]
    pub const fn is_ipv4_mapped_ipv6(&self) -> bool {
        let (addr, mask) = self.to_bits();

        // Suppress clippy a bit, because with return we have a nice aligning.
        #[allow(clippy::needless_return)]
        return addr & 0xffffffffffffffffffffffff00000000 == 0x00000000000000000000ffff00000000
            && mask & 0xffffffffffffffffffffffff00000000 == 0xffffffffffffffffffffffff00000000;
    }

    /// Converts this network to a contiguous network by clearing mask bits set
    /// to one after the first zero bit.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv6Addr;
    ///
    /// use netip::Ipv6Network;
    ///
    /// let net = Ipv6Network::new(
    ///     Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0xf800, 0, 0),
    ///     Ipv6Addr::new(0xffff, 0xffff, 0xffff, 0, 0xffff, 0xffff, 0, 0),
    /// );
    /// let expected = Ipv6Network::new(
    ///     Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0, 0, 0),
    ///     Ipv6Addr::new(0xffff, 0xffff, 0xffff, 0, 0, 0, 0, 0),
    /// );
    ///
    /// assert_eq!(expected, net.to_contiguous());
    /// ```
    pub fn to_contiguous(&self) -> Self {
        let (addr, mask) = self.to_bits();

        Self::try_from((addr.into(), mask.leading_ones() as u8)).expect("CIDR must be in range [0; 128]")
    }

    /// Converts this network to an [`IPv4` network] if it's an IPv4-mapped
    /// network, as defined in [IETF RFC 4291 section 2.5.5.2], otherwise
    /// returns [`None`].
    ///
    /// `::ffff:a.b.c.d/N` becomes `a.b.c.d/M`, where `M = N - 96`.
    /// All networks with their addresses *not* starting with `::ffff` will
    /// return `None`.
    ///
    /// [`IPv4` network]: Ipv4Network
    /// [IETF RFC 4291 section 2.5.5.2]: https://tools.ietf.org/html/rfc4291#section-2.5.5.2
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::{Ipv4Addr, Ipv6Addr};
    ///
    /// use netip::{Ipv4Network, Ipv6Network};
    ///
    /// let net = Ipv6Network::new(
    ///     Ipv6Addr::new(0, 0, 0, 0, 0, 0xffff, 0xc00a, 0x2ff),
    ///     Ipv6Addr::new(
    ///         0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xff00,
    ///     ),
    /// );
    /// let expected = Ipv4Network::new(
    ///     Ipv4Addr::new(192, 10, 2, 0),
    ///     Ipv4Addr::new(255, 255, 255, 0),
    /// );
    /// assert_eq!(Some(expected), net.to_ipv4_mapped());
    ///
    /// // This networks is IPv4-compatible, but not the IPv4-mapped one.
    /// let net = Ipv6Network::new(
    ///     Ipv6Addr::new(0, 0, 0, 0, 0, 0, 0xc00a, 0x2ff),
    ///     Ipv6Addr::new(
    ///         0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xff00,
    ///     ),
    /// );
    /// assert_eq!(None, net.to_ipv4_mapped());
    /// ```
    #[inline]
    pub const fn to_ipv4_mapped(&self) -> Option<Ipv4Network> {
        if self.is_ipv4_mapped_ipv6() {
            let (addr, mask) = self.to_bits();
            let addr: u32 = (addr & 0xffffffff) as u32;
            let mask: u32 = (mask & 0xffffffff) as u32;

            Some(Ipv4Network::from_bits(addr, mask))
        } else {
            None
        }
    }

    /// Returns the mask prefix, i.e. the number of leading bits set, if this
    /// network is a contiguous one, `None` otherwise.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::{Ipv4Network, Ipv6Network};
    ///
    /// assert_eq!(
    ///     Some(40),
    ///     Ipv6Network::parse("2a02:6b8::/40").unwrap().prefix()
    /// );
    /// assert_eq!(
    ///     Some(128),
    ///     Ipv6Network::parse("2a02:6b8::1").unwrap().prefix()
    /// );
    ///
    /// // The IPv4-mapped IPv6 equivalent is `::ffff:8.8.8.0/120`.
    /// assert_eq!(
    ///     Some(120),
    ///     Ipv4Network::parse("8.8.8.0/24")
    ///         .unwrap()
    ///         .to_ipv6_mapped()
    ///         .prefix()
    /// );
    /// ```
    #[inline]
    pub const fn prefix(&self) -> Option<u8> {
        let mask = u128::from_be_bytes(self.mask().octets());
        let ones = mask.leading_ones();

        if mask.count_ones() == ones {
            Some(ones as u8)
        } else {
            None
        }
    }

    /// Calculates and returns the `Ipv6Network`, which will be a supernet, i.e.
    /// the shortest common network both for this network and the specified
    /// ones.
    ///
    /// # Note
    ///
    /// This function works fine for both contiguous and non-contiguous
    /// networks.
    ///
    /// Use [`Ipv6Network::to_contiguous`] method if you need to convert the
    /// output network.
    ///
    /// # Examples
    ///
    /// ```
    /// use core::net::Ipv6Addr;
    ///
    /// use netip::Ipv6Network;
    ///
    /// // 2013:db8:1::1/64
    /// let net0 = Ipv6Network::new(
    ///     Ipv6Addr::new(0x2013, 0xdb8, 0x1, 0, 0, 0, 0, 0x1),
    ///     Ipv6Addr::new(0xffff, 0xffff, 0xffff, 0xffff, 0, 0, 0, 0),
    /// );
    ///
    /// // 2013:db8:2::1/64
    /// let net1 = Ipv6Network::new(
    ///     Ipv6Addr::new(0x2013, 0xdb8, 0x2, 0, 0, 0, 0, 0x1),
    ///     Ipv6Addr::new(0xffff, 0xffff, 0xffff, 0xffff, 0, 0, 0, 0),
    /// );
    ///
    /// // 2013:db8::/46
    /// let expected = Ipv6Network::new(
    ///     Ipv6Addr::new(0x2013, 0xdb8, 0, 0, 0, 0, 0, 0),
    ///     Ipv6Addr::new(0xffff, 0xffff, 0xfffc, 0, 0, 0, 0, 0),
    /// );
    /// assert_eq!(expected, net0.supernet_for(&[net1]));
    /// ```
    pub fn supernet_for(&self, nets: &[Ipv6Network]) -> Ipv6Network {
        let (addr, mask) = self.to_bits();

        let mut min = addr;
        let mut max = addr;

        for net in nets {
            let (addr, ..) = net.to_bits();
            if addr < min {
                min = addr;
            } else if addr > max {
                max = addr;
            }
        }

        let common_addr = min ^ max;
        let common_addr_len = common_addr.leading_zeros();
        let mask = mask & !(u128::MAX.checked_shr(common_addr_len).unwrap_or_default());

        Self::new(addr.into(), mask.into())
    }
}

impl Display for Ipv6Network {
    /// Formats this IPv6 network using the given formatter.
    ///
    /// The idea is to format using **the most compact** representation, except
    /// for non-contiguous networks, which are formatted explicitly.
    fn fmt(&self, fmt: &mut Formatter) -> Result<(), core::fmt::Error> {
        match self {
            Ipv6Network(addr, mask) => {
                match self.prefix() {
                    Some(prefix) => fmt.write_fmt(format_args!("{}/{}", addr, prefix)),
                    None => {
                        // This is a non-contiguous network, expand the mask.
                        fmt.write_fmt(format_args!("{}/{}", addr, mask))
                    }
                }
            }
        }
    }
}

impl From<Ipv4Network> for Ipv6Network {
    #[inline]
    fn from(net: Ipv4Network) -> Self {
        net.to_ipv6_mapped()
    }
}

impl From<Ipv6Addr> for Ipv6Network {
    fn from(addr: Ipv6Addr) -> Self {
        Ipv6Network(addr, IPV6_ALL_BITS)
    }
}

impl TryFrom<(Ipv6Addr, u8)> for Ipv6Network {
    type Error = CidrOverflowError;

    fn try_from((addr, cidr): (Ipv6Addr, u8)) -> Result<Self, Self::Error> {
        let mask = ipv6_mask_from_cidr(cidr)?;
        Ok(Ipv6Network::new(addr, mask))
    }
}

impl FromStr for Ipv6Network {
    type Err = IpNetParseError;

    #[inline]
    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Ipv6Network::parse(s)
    }
}

impl AsRef<Ipv6Network> for Ipv6Network {
    #[inline]
    fn as_ref(&self) -> &Ipv6Network {
        self
    }
}

/// Finds the shortest common network for the given slice of networks that
/// covers at least half of slice.
///
/// Shortest means that it will have the shortest segment of subnets, or the
/// largest mask.
///
/// Imagine that you are building a radix tree or graph from networks and at
/// some point you need to split one of the nodes into two parts.
///
/// In other words, it is necessary to find the shortest network that contains
/// at least half of the child networks.
///
/// This function implements such an algorithm.
///
/// For example, suppose we have the following tree:
/// ```text
/// .
/// ├─ 127.0.0.1
/// ├─ 127.0.0.2
/// └─ 127.0.0.3
/// ```
///
/// After splitting:
///
/// ```text
/// .
/// ├─ 127.0.0.1
/// └─ 127.0.0.2/31
///   ├─ 127.0.0.2
///   └─ 127.0.0.3
/// ```
///
/// Another more interesting example:
///
/// ```text
/// .
/// ├─ 10.204.46.54/31
/// ├─ 10.204.46.68/31
/// ├─ 10.204.49.12/31
/// ├─ 10.204.49.14/31
/// ├─ 10.204.49.18/31
/// ├─ 10.204.49.32/31
/// ├─ 10.204.49.34/31
/// ├─ 10.204.49.36/31
/// ├─ 10.204.49.38/31
/// └─ 10.204.49.40/31
/// ```
///
/// After splitting:
///
/// ```text
/// .
/// ├─ 10.204.46.50/31
/// ├─ 10.204.46.54/31
/// ├─ 10.204.47.22/31
/// ├─ 10.204.49.0/26 [*]
/// │ ├─ 10.204.49.24/31
/// │ ├─ 10.204.49.26/31
/// │ ├─ 10.204.49.28/31
/// │ ├─ 10.204.49.32/31
/// │ ├─ 10.204.49.34/31
/// │ └─ 10.204.49.36/31
/// ├─ 10.204.50.38/31
/// └─ 10.204.50.40/31
/// ```
///
/// # Complexity
///
/// This function runs in `O(N)` and requires `O(1)` memory.
///
/// # Warning
///
/// The specified slice must be sorted, otherwise the result is unspecified and
/// meaningless.
///
/// Specified networks must not overlap.
///
/// # Examples
///
/// ```
/// use netip::{Ipv4Network, ipv4_binary_split};
///
/// let nets = &["127.0.0.1", "127.0.0.2", "127.0.0.3"];
/// let mut nets: Vec<_> = nets
///     .iter()
///     .map(|net| Ipv4Network::parse(net).unwrap())
///     .collect();
/// nets.sort();
/// nets.dedup();
///
/// let (supernet, range) = ipv4_binary_split(&nets).unwrap();
/// // 127.0.0.2/31 in binary is "0b...10/0b...10", which contains both "127.0.0.2" and "127.0.0.3".
/// assert_eq!(Ipv4Network::parse("127.0.0.2/31").unwrap(), supernet);
///
/// for (idx, net) in nets.iter().enumerate() {
///     assert_eq!(range.contains(&idx), supernet.contains(net));
/// }
///
/// // For the second example above.
///
/// let nets = &[
///     "10.204.46.50/31",
///     "10.204.46.54/31",
///     "10.204.47.22/31",
///     "10.204.49.24/31",
///     "10.204.49.26/31",
///     "10.204.49.28/31",
///     "10.204.49.32/31",
///     "10.204.49.34/31",
///     "10.204.49.36/31",
///     "10.204.50.38/31",
///     "10.204.50.40/31",
/// ];
/// let mut nets: Vec<_> = nets
///     .iter()
///     .map(|net| Ipv4Network::parse(net).unwrap())
///     .collect();
/// nets.sort();
/// nets.dedup();
///
/// let (supernet, range) = ipv4_binary_split(&nets).unwrap();
///
/// assert_eq!(Ipv4Network::parse("10.204.49.0/26").unwrap(), supernet);
///
/// for (idx, net) in nets.iter().enumerate() {
///     assert_eq!(range.contains(&idx), supernet.contains(net));
/// }
/// ```
pub fn ipv4_binary_split<T>(nets: &[T]) -> Option<(Ipv4Network, Range<usize>)>
where
    T: AsRef<Ipv4Network>,
{
    ip_binary_split(nets)
}

/// Finds the shortest common network for the given slice of networks that
/// covers at least half of slice.
///
/// Shortest means that it will have the shortest segment of subnets, or the
/// largest mask.
///
/// Imagine that you are building a radix tree or graph from networks and at
/// some point you need to split one of the nodes into two parts.
///
/// In other words, it is necessary to find the shortest network that contains
/// at least half of the child networks.
///
/// This function implements such an algorithm.
///
/// For example, suppose we have the following tree:
/// ```text
/// .
/// ├─ ::1
/// ├─ ::2
/// └─ ::3
/// ```
///
/// After splitting:
///
/// ```text
/// .
/// ├─ ::1
/// └─ ::2/127
///   ├─ ::2
///   └─ ::3
/// ```
///
/// Another more interesting example:
///
/// ```text
/// .
/// ├─ 2a02:6b8:0:2302::/64
/// ├─ 2a02:6b8:0:2303::/64
/// ├─ 2a02:6b8:0:2305::/64
/// ├─ 2a02:6b8:0:2308::/64
/// ├─ 2a02:6b8:0:2309::/64
/// ├─ 2a02:6b8:0:230a::/64
/// ├─ 2a02:6b8:0:230b::/64
/// ├─ 2a02:6b8:0:230c::/64
/// ├─ 2a02:6b8:0:230d::/64
/// ├─ 2a02:6b8:0:2314::/64
/// └─ 2a02:6b8:0:231f::/64
/// ```
///
/// After splitting:
///
/// ```text
/// .
/// ├─ 2a02:6b8:0:2302::/64
/// ├─ 2a02:6b8:0:2303::/64
/// ├─ 2a02:6b8:0:2305::/64
/// └─ 2a02:6b8:0:2308::/61
/// │ ├─ 2a02:6b8:0:2308::/64
/// │ ├─ 2a02:6b8:0:2309::/64
/// │ ├─ 2a02:6b8:0:230a::/64
/// │ ├─ 2a02:6b8:0:230b::/64
/// │ ├─ 2a02:6b8:0:230c::/64
/// │ └─ 2a02:6b8:0:230d::/64
/// ├─ 2a02:6b8:0:2314::/64
/// └─ 2a02:6b8:0:231f::/64
/// ```
///
/// # Complexity
///
/// This function runs in `O(N)` and requires `O(1)` memory.
///
/// # Warning
///
/// The specified slice must be sorted, otherwise the result is unspecified and
/// meaningless.
///
/// Specified networks must not overlap.
///
/// # Examples
///
/// ```
/// use netip::{Ipv6Network, ipv6_binary_split};
///
/// let nets = &["::1", "::2", "::3"];
/// let mut nets: Vec<_> = nets
///     .iter()
///     .map(|net| Ipv6Network::parse(net).unwrap())
///     .collect();
///
/// // The input must be sorted.
/// nets.sort();
///
/// let (supernet, range) = ipv6_binary_split(&nets).unwrap();
/// // ::2/127 in binary is "0b...10/0b...10", which contains both "::2" and "::3".
/// assert_eq!(Ipv6Network::parse("::2/127").unwrap(), supernet);
///
/// for (idx, net) in nets.iter().enumerate() {
///     assert_eq!(range.contains(&idx), supernet.contains(net));
/// }
///
/// // For the second example above.
///
/// let nets = &[
///     "2a02:6b8:0:2302::/64",
///     "2a02:6b8:0:2303::/64",
///     "2a02:6b8:0:2305::/64",
///     "2a02:6b8:0:2308::/64",
///     "2a02:6b8:0:2309::/64",
///     "2a02:6b8:0:230a::/64",
///     "2a02:6b8:0:230b::/64",
///     "2a02:6b8:0:230c::/64",
///     "2a02:6b8:0:230d::/64",
///     "2a02:6b8:0:2314::/64",
///     "2a02:6b8:0:231f::/64",
/// ];
/// let mut nets: Vec<_> = nets
///     .iter()
///     .map(|net| Ipv6Network::parse(net).unwrap())
///     .collect();
/// nets.sort();
///
/// let (supernet, range) = ipv6_binary_split(&nets).unwrap();
/// assert_eq!(
///     Ipv6Network::parse("2a02:6b8:0:2308::/61").unwrap(),
///     supernet
/// );
///
/// for (idx, net) in nets.iter().enumerate() {
///     assert_eq!(range.contains(&idx), supernet.contains(net));
/// }
/// ```
pub fn ipv6_binary_split<T>(nets: &[T]) -> Option<(Ipv6Network, Range<usize>)>
where
    T: AsRef<Ipv6Network>,
{
    ip_binary_split(nets)
}

#[inline]
fn ip_binary_split<T, U>(nets: &[T]) -> Option<(U, Range<usize>)>
where
    T: AsRef<U>,
    U: BinarySplit + Copy,
{
    if nets.len() < 3 {
        return None;
    }

    // Window size.
    let size = nets.len().div_ceil(2);

    // We start with supernet that covers all given networks.
    let mut range = 0..nets.len() - 1;
    let mut candidate = nets[range.start].as_ref().supernet_for(&[*nets[range.end].as_ref()]);

    for (idx, window) in nets.windows(size).enumerate() {
        let supernet = window[0].as_ref().supernet_for(&[*window[size - 1].as_ref()]);

        if supernet.mask() > candidate.mask() {
            range = idx..(idx + size);
            candidate = supernet;
        }
    }

    // Adjust range from both sides, since our supernet may cover more networks.
    while range.start > 0 {
        if candidate.contains(nets[range.start - 1].as_ref()) {
            range.start -= 1;
        } else {
            break;
        }
    }
    while range.end < nets.len() {
        // Remember, range end is exclusive.
        if candidate.contains(nets[range.end].as_ref()) {
            range.end += 1;
        } else {
            break;
        }
    }

    Some((candidate, range))
}

trait BinarySplit: Sized {
    type Mask: PartialOrd;

    fn mask(&self) -> &Self::Mask;
    fn contains(&self, other: &Self) -> bool;
    fn supernet_for(&self, nets: &[Self]) -> Self;
}

impl BinarySplit for Ipv4Network {
    type Mask = Ipv4Addr;

    #[inline]
    fn mask(&self) -> &Self::Mask {
        self.mask()
    }

    #[inline]
    fn contains(&self, other: &Self) -> bool {
        self.contains(other)
    }

    #[inline]
    fn supernet_for(&self, nets: &[Self]) -> Ipv4Network {
        self.supernet_for(nets)
    }
}

impl BinarySplit for Ipv6Network {
    type Mask = Ipv6Addr;

    #[inline]
    fn mask(&self) -> &Self::Mask {
        self.mask()
    }

    #[inline]
    fn contains(&self, other: &Self) -> bool {
        self.contains(other)
    }

    #[inline]
    fn supernet_for(&self, nets: &[Self]) -> Ipv6Network {
        self.supernet_for(nets)
    }
}

/// An error that occurs when parsing a contiguous IP network.
#[derive(Debug, PartialEq)]
pub enum ContiguousIpNetParseError {
    /// The network is not contiguous.
    NonContiguousNetwork,
    /// An error occurred while parsing the IP network.
    IpNetParseError(IpNetParseError),
}

impl From<IpNetParseError> for ContiguousIpNetParseError {
    #[inline]
    fn from(err: IpNetParseError) -> Self {
        ContiguousIpNetParseError::IpNetParseError(err)
    }
}

impl Display for ContiguousIpNetParseError {
    fn fmt(&self, fmt: &mut Formatter) -> Result<(), core::fmt::Error> {
        match self {
            Self::NonContiguousNetwork => write!(fmt, "non-contiguous network"),
            Self::IpNetParseError(err) => write!(fmt, "{}", err),
        }
    }
}

impl Error for ContiguousIpNetParseError {}

/// A contiguous IP network.
///
/// Represents a wrapper around an IP network that is **guaranteed to be
/// contiguous**.
#[derive(Debug, Copy, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Contiguous<T>(T);

impl Contiguous<IpNetwork> {
    /// Parses an IP network from a string and returns a contiguous network.
    pub fn parse(buf: &str) -> Result<Self, ContiguousIpNetParseError> {
        let net = IpNetwork::parse(buf)?;

        if net.is_contiguous() {
            Ok(Self(net))
        } else {
            Err(ContiguousIpNetParseError::NonContiguousNetwork)
        }
    }

    /// Returns the prefix length of the network.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::{Contiguous, IpNetwork};
    ///
    /// let net = Contiguous::<IpNetwork>::parse("192.168.1.0/24").unwrap();
    /// assert_eq!(24, net.prefix());
    /// ```
    #[inline]
    pub const fn prefix(&self) -> u8 {
        match self {
            Self(IpNetwork::V4(net)) => Contiguous(*net).prefix(),
            Self(IpNetwork::V6(net)) => Contiguous(*net).prefix(),
        }
    }
}

impl Contiguous<Ipv4Network> {
    /// Parses an IPv4 network from a string and returns a contiguous network.
    pub fn parse(buf: &str) -> Result<Self, ContiguousIpNetParseError> {
        let net = Ipv4Network::parse(buf)?;

        if net.is_contiguous() {
            Ok(Self(net))
        } else {
            Err(ContiguousIpNetParseError::NonContiguousNetwork)
        }
    }

    /// Returns the prefix length of the network.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::{Contiguous, Ipv4Network};
    ///
    /// let net = Contiguous::<Ipv4Network>::parse("172.16.0.0/12").unwrap();
    /// assert_eq!(12, net.prefix());
    /// ```
    #[inline]
    pub const fn prefix(&self) -> u8 {
        match self {
            Self(net) => {
                let mask = u32::from_be_bytes(net.mask().octets());
                let ones = mask.leading_ones();
                ones as u8
            }
        }
    }
}

impl Contiguous<Ipv6Network> {
    /// Parses an IPv6 network from a string and returns a contiguous network.
    pub fn parse(buf: &str) -> Result<Self, ContiguousIpNetParseError> {
        let net = Ipv6Network::parse(buf)?;

        if net.is_contiguous() {
            Ok(Self(net))
        } else {
            Err(ContiguousIpNetParseError::NonContiguousNetwork)
        }
    }

    /// Returns the prefix length of the network.
    ///
    /// # Examples
    ///
    /// ```
    /// use netip::{Contiguous, Ipv6Network};
    ///
    /// let net = Contiguous::<Ipv6Network>::parse("2a02:6b8:c00::/40").unwrap();
    /// assert_eq!(40, net.prefix());
    /// ```
    #[inline]
    pub const fn prefix(&self) -> u8 {
        match self {
            Self(net) => {
                let mask = u128::from_be_bytes(net.mask().octets());
                let ones = mask.leading_ones();
                ones as u8
            }
        }
    }
}

impl<T> Deref for Contiguous<T> {
    type Target = T;

    #[inline]
    fn deref(&self) -> &Self::Target {
        match self {
            Self(net) => net,
        }
    }
}

impl<T> Display for Contiguous<T>
where
    T: Display,
{
    #[inline]
    fn fmt(&self, fmt: &mut Formatter) -> Result<(), core::fmt::Error> {
        match self {
            Self(net) => Display::fmt(net, fmt),
        }
    }
}

impl FromStr for Contiguous<IpNetwork> {
    type Err = ContiguousIpNetParseError;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Self::parse(s)
    }
}

impl FromStr for Contiguous<Ipv4Network> {
    type Err = ContiguousIpNetParseError;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Self::parse(s)
    }
}

impl FromStr for Contiguous<Ipv6Network> {
    type Err = ContiguousIpNetParseError;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Self::parse(s)
    }
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn test_ipv4_mask_from_cidr() {
        let masks = &[
            0u32,
            0b10000000000000000000000000000000,
            0b11000000000000000000000000000000,
            0b11100000000000000000000000000000,
            0b11110000000000000000000000000000,
            0b11111000000000000000000000000000,
            0b11111100000000000000000000000000,
            0b11111110000000000000000000000000,
            0b11111111000000000000000000000000,
            0b11111111100000000000000000000000,
            0b11111111110000000000000000000000,
            0b11111111111000000000000000000000,
            0b11111111111100000000000000000000,
            0b11111111111110000000000000000000,
            0b11111111111111000000000000000000,
            0b11111111111111100000000000000000,
            0b11111111111111110000000000000000,
            0b11111111111111111000000000000000,
            0b11111111111111111100000000000000,
            0b11111111111111111110000000000000,
            0b11111111111111111111000000000000,
            0b11111111111111111111100000000000,
            0b11111111111111111111110000000000,
            0b11111111111111111111111000000000,
            0b11111111111111111111111100000000,
            0b11111111111111111111111110000000,
            0b11111111111111111111111111000000,
            0b11111111111111111111111111100000,
            0b11111111111111111111111111110000,
            0b11111111111111111111111111111000,
            0b11111111111111111111111111111100,
            0b11111111111111111111111111111110,
            0b11111111111111111111111111111111,
        ];

        for (idx, &mask) in masks.iter().enumerate() {
            assert_eq!(Ipv4Addr::from(mask), ipv4_mask_from_cidr(idx as u8).unwrap());
        }

        assert!(ipv4_mask_from_cidr(33).is_err());
    }

    #[test]
    fn test_ipv6_mask_from_cidr() {
        assert_eq!(Ipv6Addr::UNSPECIFIED, ipv6_mask_from_cidr(0).unwrap());
        assert_eq!(
            Ipv6Addr::new(0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0x8000, 0),
            ipv6_mask_from_cidr(97).unwrap()
        );
        assert_eq!(
            Ipv6Addr::new(0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff),
            ipv6_mask_from_cidr(128).unwrap()
        );

        assert!(ipv6_mask_from_cidr(129).is_err());
    }

    #[test]
    fn parse_ipv4() {
        assert_eq!(
            Ipv4Network::new("127.0.0.1".parse().unwrap(), "255.255.255.255".parse().unwrap()),
            Ipv4Network::parse("127.0.0.1").unwrap()
        );
    }

    #[test]
    fn parse_ipv4_cidr() {
        assert_eq!(
            Ipv4Network::new("192.168.0.0".parse().unwrap(), "255.255.255.0".parse().unwrap()),
            Ipv4Network::parse("192.168.0.1/24").unwrap()
        );

        assert_eq!(
            Ipv4Network::new("192.168.0.1".parse().unwrap(), "255.255.255.255".parse().unwrap()),
            Ipv4Network::parse("192.168.0.1/32").unwrap()
        );
    }

    #[test]
    fn parse_ipv4_explicit_mask() {
        assert_eq!(
            Ipv4Network::new("192.168.0.0".parse().unwrap(), "255.255.255.0".parse().unwrap()),
            Ipv4Network::parse("192.168.0.1/255.255.255.0").unwrap()
        );
    }

    #[test]
    fn ipv4_contains() {
        assert!(
            Ipv4Network::parse("0.0.0.0/0")
                .unwrap()
                .contains(&Ipv4Network::parse("127.0.0.1").unwrap())
        );
        assert!(
            Ipv4Network::parse("0.0.0.0/8")
                .unwrap()
                .contains(&Ipv4Network::parse("0.0.0.0/9").unwrap())
        );
    }

    #[test]
    fn ipv4_not_contains() {
        assert!(
            !Ipv4Network::parse("0.0.0.0/9")
                .unwrap()
                .contains(&Ipv4Network::parse("0.0.0.0/8").unwrap())
        );
    }

    #[test]
    fn ipv4_contains_self() {
        assert!(
            Ipv4Network::parse("127.0.0.1")
                .unwrap()
                .contains(&Ipv4Network::parse("127.0.0.1").unwrap())
        );
    }

    #[test]
    fn ipv4_supernet_for() {
        let net0 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 0), 25)).unwrap();
        let net1 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 128), 25)).unwrap();

        let expected = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 0), 24)).unwrap();
        assert_eq!(expected, net0.supernet_for(&[net1]));
        assert_eq!(expected, net1.supernet_for(&[net0]));
    }

    #[test]
    fn ipv4_supernet_for_equal_nets() {
        let net0 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 128), 25)).unwrap();
        let net1 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 128), 25)).unwrap();

        let expected = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 128), 25)).unwrap();
        assert_eq!(expected, net0.supernet_for(&[net1]));
        assert_eq!(expected, net1.supernet_for(&[net0]));
    }

    #[test]
    fn ipv4_supernet_for_many() {
        let net0 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 0), 27)).unwrap();
        let net1 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 32), 27)).unwrap();
        let net2 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 64), 27)).unwrap();
        let net3 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 96), 27)).unwrap();
        let net4 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 128), 27)).unwrap();
        let net5 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 160), 27)).unwrap();
        let net6 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 192), 27)).unwrap();
        let net7 = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 224), 27)).unwrap();

        let expected = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 0), 24)).unwrap();
        assert_eq!(expected, net0.supernet_for(&[net1, net2, net3, net4, net5, net6, net7]));
        assert_eq!(expected, net1.supernet_for(&[net0, net2, net3, net4, net5, net6, net7]));
        assert_eq!(expected, net2.supernet_for(&[net0, net1, net3, net4, net5, net6, net7]));
        assert_eq!(expected, net3.supernet_for(&[net0, net1, net2, net4, net5, net6, net7]));
        assert_eq!(expected, net4.supernet_for(&[net0, net1, net2, net3, net5, net6, net7]));
        assert_eq!(expected, net5.supernet_for(&[net0, net1, net2, net3, net4, net6, net7]));
        assert_eq!(expected, net6.supernet_for(&[net0, net1, net2, net3, net4, net5, net7]));
        assert_eq!(expected, net7.supernet_for(&[net0, net1, net2, net3, net4, net5, net6]));
    }

    #[test]
    fn ipv4_supernet_for_many_more() {
        let nets = &[
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 0), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 16), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 32), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 48), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 64), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 80), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 96), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 112), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 128), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 144), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 160), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 176), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 192), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 208), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 224), 28)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 240), 28)).unwrap(),
        ];

        let expected = Ipv4Network::try_from((Ipv4Addr::new(192, 0, 2, 0), 24)).unwrap();
        assert_eq!(expected, nets[0].supernet_for(&nets[1..]).to_contiguous());
    }

    #[test]
    fn ipv4_supernet_for_wide() {
        let nets = &[
            Ipv4Network::try_from((Ipv4Addr::new(10, 40, 101, 1), 32)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(10, 40, 102, 1), 32)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(10, 40, 103, 1), 32)).unwrap(),
        ];

        let expected = Ipv4Network::try_from((Ipv4Addr::new(10, 40, 100, 0), 22)).unwrap();
        assert_eq!(expected, nets[0].supernet_for(&nets[1..]).to_contiguous());
    }

    #[test]
    fn ipv4_supernet_for_wide_more() {
        let nets = &[
            Ipv4Network::try_from((Ipv4Addr::new(10, 40, 101, 1), 32)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(10, 40, 102, 1), 32)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(11, 40, 103, 1), 32)).unwrap(),
        ];

        let expected = Ipv4Network::try_from((Ipv4Addr::new(10, 0, 0, 0), 7)).unwrap();
        assert_eq!(expected, nets[0].supernet_for(&nets[1..]).to_contiguous());
    }

    #[test]
    fn ipv4_supernet_for_min() {
        let nets = &[
            Ipv4Network::try_from((Ipv4Addr::new(192, 168, 0, 0), 24)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 168, 1, 0), 24)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 168, 2, 0), 24)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 168, 100, 0), 24)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 168, 200, 0), 24)).unwrap(),
        ];

        let expected = Ipv4Network::try_from((Ipv4Addr::new(192, 168, 0, 0), 16)).unwrap();
        assert_eq!(expected, nets[0].supernet_for(&nets[1..]).to_contiguous());
    }

    #[test]
    fn ipv4_supernet_for_unspecified() {
        let nets = &[
            Ipv4Network::try_from((Ipv4Addr::new(128, 0, 0, 0), 24)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(192, 0, 0, 0), 24)).unwrap(),
            Ipv4Network::try_from((Ipv4Addr::new(65, 0, 0, 0), 24)).unwrap(),
        ];

        let expected = Ipv4Network::try_from((Ipv4Addr::new(0, 0, 0, 0), 0)).unwrap();
        assert_eq!(expected, nets[0].supernet_for(&nets[1..]).to_contiguous());
    }

    #[test]
    fn ipv4_supernet_for_non_contiguous() {
        let nets = &[
            Ipv4Network::new(Ipv4Addr::new(10, 40, 0, 1), Ipv4Addr::new(255, 255, 0, 255)),
            Ipv4Network::new(Ipv4Addr::new(10, 40, 0, 2), Ipv4Addr::new(255, 255, 0, 255)),
            Ipv4Network::new(Ipv4Addr::new(11, 40, 0, 3), Ipv4Addr::new(255, 255, 0, 255)),
        ];

        let expected = Ipv4Network::try_from((Ipv4Addr::new(10, 0, 0, 0), 7)).unwrap();
        assert_eq!(expected, nets[0].supernet_for(&nets[1..]).to_contiguous());
    }

    #[test]
    fn parse_ipv6_explicit_mask_normalizes_addr() {
        // Address bits outside the mask should be cleared.
        assert_eq!(
            Ipv6Network::new(
                Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0, 0, 0),
                Ipv6Addr::new(0xffff, 0xffff, 0xff00, 0, 0, 0, 0, 0),
            ),
            Ipv6Network::parse("2a02:6b8:c00:1:2:3:4:5/ffff:ffff:ff00::").unwrap()
        );
    }

    #[test]
    fn test_ipv6_addr_as_u128() {
        assert_eq!(
            0x2a02_06b8_0c00_0000_0000_f800_0000_0000u128,
            (*Ipv6Network::parse("2a02:6b8:c00::f800:0:0/ffff:ffff:ff00:0:ffff:f800::")
                .unwrap()
                .addr())
            .into()
        );
    }

    #[test]
    fn test_net_ipv6_contains() {
        assert!(
            Ipv6Network::parse("::/0")
                .unwrap()
                .contains(&Ipv6Network::parse("::1").unwrap())
        );
        assert!(
            Ipv6Network::parse("::/32")
                .unwrap()
                .contains(&Ipv6Network::parse("::/33").unwrap())
        );

        assert!(
            !Ipv6Network::parse("::/33")
                .unwrap()
                .contains(&Ipv6Network::parse("::/32").unwrap())
        );
    }

    #[test]
    fn test_net_ipv6_contains_self() {
        assert!(
            Ipv6Network::parse("::1")
                .unwrap()
                .contains(&Ipv6Network::parse("::1").unwrap())
        );
    }

    #[test]
    fn test_net_ipv6_non_contiguous_contains_contiguous() {
        assert!(
            Ipv6Network::parse("2a02:6b8:c00::4d71:0:0/ffff:ffff:ff00::ffff:ffff:0:0")
                .unwrap()
                .contains(&Ipv6Network::parse("2a02:6b8:c00:1234:0:4d71::1").unwrap())
        );
    }

    #[test]
    fn test_net_ipv6_is_contiguous() {
        assert!(Ipv6Network::parse("::/0").unwrap().is_contiguous());
        assert!(Ipv6Network::parse("::1").unwrap().is_contiguous());
        assert!(Ipv6Network::parse("2a02:6b8:c00::/40").unwrap().is_contiguous());
        assert!(
            Ipv6Network::parse("2a02:6b8:c00:1:2:3:4:1/127")
                .unwrap()
                .is_contiguous()
        );
        assert!(
            Ipv6Network::parse("2a02:6b8:c00:1:2:3:4:1/128")
                .unwrap()
                .is_contiguous()
        );
        assert!(Ipv6Network::parse("2a02:6b8:c00:1:2:3:4:1").unwrap().is_contiguous());

        assert!(
            !Ipv6Network::parse("2a02:6b8:c00::f800:0:0/ffff:ffff:ff00::ffff:ffff:0:0")
                .unwrap()
                .is_contiguous()
        );
        assert!(
            !Ipv6Network::parse("2a02:6b8:c00::f800:0:0/ffff:ffff:ff00:0:ffff:f800::")
                .unwrap()
                .is_contiguous()
        );
        assert!(
            !Ipv6Network::parse("2a02:6b8:0:0:1234:5678::/ffff:ffff:0:0:ffff:ffff:0:0")
                .unwrap()
                .is_contiguous()
        );
        assert!(
            !Ipv6Network::parse("2a02:6b8:0:0:1234:5678::/ffff:ffff:0:0:f0f0:f0f0:f0f0:f0f0")
                .unwrap()
                .is_contiguous()
        );
    }

    #[test]
    fn ipv6_supernet_for() {
        let nets = &[
            Ipv6Network::parse("2001:db8:1::/32").unwrap(),
            Ipv6Network::parse("2001:db8:2::/39").unwrap(),
        ];

        let expected = Ipv6Network::parse("2001:db8::/32").unwrap();
        assert_eq!(expected, nets[0].supernet_for(&nets[1..]));
    }

    #[test]
    fn ipv6_supernet_for_wide() {
        let nets = &[
            Ipv6Network::parse("2013:db8:1::1/64").unwrap(),
            Ipv6Network::parse("2013:db8:2::1/64").unwrap(),
        ];

        let expected = Ipv6Network::parse("2013:db8::/46").unwrap();
        assert_eq!(expected, nets[0].supernet_for(&nets[1..]));
    }

    #[test]
    fn ipv6_supernet_for_unspecified() {
        let nets = &[
            Ipv6Network::parse("8001:db8:1::/34").unwrap(),
            Ipv6Network::parse("2013:db8:2::/32").unwrap(),
        ];

        let expected = Ipv6Network::parse("::/0").unwrap();
        assert_eq!(expected, nets[0].supernet_for(&nets[1..]));
    }

    #[test]
    fn ipv6_supernet_for_non_contiguous() {
        let nets = &[
            Ipv6Network::parse("2a02:6b8:c00::48aa:0:0/ffff:ffff:ff00::ffff:ffff:0:0").unwrap(),
            Ipv6Network::parse("2a02:6b8:c00::4707:0:0/ffff:ffff:ff00::ffff:ffff:0:0").unwrap(),
        ];

        let expected = Ipv6Network::parse("2a02:6b8:c00::4000:0:0/ffff:ffff:ff00::ffff:f000:0:0").unwrap();
        assert_eq!(expected, nets[0].supernet_for(&nets[1..]));
    }

    #[test]
    fn ipv6_supernet_for_non_contiguous_different_aggregates() {
        let nets = &[
            Ipv6Network::parse("2a02:6b8:c00::48aa:0:0/ffff:ffff:ff00::ffff:ffff:0:0").unwrap(),
            Ipv6Network::parse("2a02:6b8:fc00::4707:0:0/ffff:ffff:ff00::ffff:ffff:0:0").unwrap(),
        ];

        let expected = Ipv6Network::parse("2a02:6b8::/32").unwrap();
        assert_eq!(expected, nets[0].supernet_for(&nets[1..]));
    }

    #[test]
    fn test_net_display_addr() {
        let net = Ipv4Network::new(Ipv4Addr::new(127, 0, 0, 1), Ipv4Addr::new(255, 255, 255, 255));
        assert_eq!("127.0.0.1/32", &format!("{net}"));

        let net = Ipv6Network::new(
            Ipv6Addr::new(0x2a02, 0x6b8, 0, 0, 0, 0, 0, 1),
            Ipv6Addr::new(0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff, 0xffff),
        );
        assert_eq!("2a02:6b8::1/128", &format!("{net}"));
    }

    #[test]
    fn test_net_display_cidr() {
        let net = Ipv4Network::parse("10.0.0.0/24").unwrap();
        assert_eq!("10.0.0.0/24", &format!("{net}"));

        let net = Ipv6Network::parse("2a02:6b8:c00:0:1:2::/96").unwrap();
        assert_eq!("2a02:6b8:c00:0:1:2::/96", &format!("{net}"));
    }

    #[test]
    fn test_net_display_non_contiguous() {
        let net = Ipv4Network::parse("10.0.1.0/255.0.255.0").unwrap();
        assert_eq!("10.0.1.0/255.0.255.0", &format!("{net}"));

        let net = Ipv6Network(
            "2a02:6b8:0:0:0:1234::".parse().unwrap(),
            "ffff:ffff:0:0:ffff:ffff:0:0".parse().unwrap(),
        );
        assert_eq!("2a02:6b8::1234:0:0/ffff:ffff::ffff:ffff:0:0", &format!("{net}"));

        let net = Ipv6Network(
            "2a02:6b8:0:0:1234:5678::".parse().unwrap(),
            "ffff:ffff:0:0:ffff:ffff:0:0".parse().unwrap(),
        );
        assert_eq!("2a02:6b8::1234:5678:0:0/ffff:ffff::ffff:ffff:0:0", &format!("{net}"));
    }

    #[test]
    fn test_ipv4_binary_split_empty() {
        assert!(ipv4_binary_split::<Ipv4Network>(&[]).is_none());
    }

    #[test]
    fn test_ipv4_binary_split_less_than_required_networks() {
        let nets = &["127.0.0.1", "127.0.0.2"];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv4Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        assert!(ipv4_binary_split(&nets).is_none());
    }

    #[test]
    fn test_ipv4_binary_split_three_nets() {
        let nets = &["127.0.0.1", "127.0.0.2", "127.0.0.3"];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv4Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        let (supernet, range) = ipv4_binary_split(&nets).unwrap();
        // 127.0.0.2/31 in binary is "0b...10/0b...10", which contains both "127.0.0.2"
        // and "127.0.0.3".
        assert_eq!(Ipv4Network::parse("127.0.0.2/31").unwrap(), supernet);

        for (idx, net) in nets.iter().enumerate() {
            assert_eq!(range.contains(&idx), supernet.contains(net));
        }
    }

    #[test]
    fn test_ipv4_binary_split_more_nets() {
        // .
        // ├─ 10.204.46.68/31
        // ├─ 10.204.49.12/31
        // ├─ 10.204.49.14/31
        // ├─ 10.204.49.18/31
        // └─ 10.204.49.32/28 [*]
        //   ├─ 10.204.49.32/31
        //   ├─ 10.204.49.34/31
        //   ├─ 10.204.49.36/31
        //   ├─ 10.204.49.38/31
        //   └─ 10.204.49.40/31

        let nets = &[
            "10.204.46.68/31",
            "10.204.49.12/31",
            "10.204.49.14/31",
            "10.204.49.18/31",
            "10.204.49.32/31",
            "10.204.49.34/31",
            "10.204.49.36/31",
            "10.204.49.38/31",
            "10.204.49.40/31",
        ];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv4Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        let (supernet, range) = ipv4_binary_split(&nets).unwrap();

        assert_eq!(Ipv4Network::parse("10.204.49.32/28").unwrap(), supernet);

        for (idx, net) in nets.iter().enumerate() {
            assert_eq!(range.contains(&idx), supernet.contains(net));
        }
    }

    #[test]
    fn test_ipv4_binary_split_even_number_of_nets() {
        // .
        // ├─ 10.204.46.54/31
        // ├─ 10.204.46.68/31
        // ├─ 10.204.49.12/31
        // ├─ 10.204.49.14/31
        // ├─ 10.204.49.18/31
        // └─ 10.204.49.32/28 [*]
        //   ├─ 10.204.49.32/31
        //   ├─ 10.204.49.34/31
        //   ├─ 10.204.49.36/31
        //   ├─ 10.204.49.38/31
        //   └─ 10.204.49.40/31

        let nets = &[
            "10.204.46.54/31",
            "10.204.46.68/31",
            "10.204.49.12/31",
            "10.204.49.14/31",
            "10.204.49.18/31",
            "10.204.49.32/31",
            "10.204.49.34/31",
            "10.204.49.36/31",
            "10.204.49.38/31",
            "10.204.49.40/31",
        ];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv4Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        let (supernet, range) = ipv4_binary_split(&nets).unwrap();

        assert_eq!(Ipv4Network::parse("10.204.49.32/28").unwrap(), supernet);

        for (idx, net) in nets.iter().enumerate() {
            assert_eq!(range.contains(&idx), supernet.contains(net));
        }
    }

    #[test]
    fn test_ipv4_binary_split_in_the_middle() {
        // .
        // ├─ 10.204.46.50/31
        // ├─ 10.204.46.54/31
        // ├─ 10.204.47.22/31
        // ├─ 10.204.49.0/26 [*]
        // │ ├─ 10.204.49.24/31
        // │ ├─ 10.204.49.26/31
        // │ ├─ 10.204.49.28/31
        // │ ├─ 10.204.49.32/31
        // │ ├─ 10.204.49.34/31
        // │ └─ 10.204.49.36/31
        // ├─ 10.204.50.38/31
        // └─ 10.204.50.40/31

        let nets = &[
            "10.204.46.50/31",
            "10.204.46.54/31",
            "10.204.47.22/31",
            "10.204.49.24/31",
            "10.204.49.26/31",
            "10.204.49.28/31",
            "10.204.49.32/31",
            "10.204.49.34/31",
            "10.204.49.36/31",
            "10.204.50.38/31",
            "10.204.50.40/31",
        ];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv4Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        let (supernet, range) = ipv4_binary_split(&nets).unwrap();

        assert_eq!(Ipv4Network::parse("10.204.49.0/26").unwrap(), supernet);

        for (idx, net) in nets.iter().enumerate() {
            assert_eq!(range.contains(&idx), supernet.contains(net));
        }
    }

    #[test]
    fn test_ipv6_binary_split_empty() {
        assert!(ipv6_binary_split::<Ipv6Network>(&[]).is_none());
    }

    #[test]
    fn test_ipv6_binary_split_less_than_required_networks() {
        let nets = &["::1", "::2"];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv6Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        assert!(ipv6_binary_split(&nets).is_none());
    }

    #[test]
    fn test_ipv6_binary_split_three_nets() {
        let nets = &["::1", "::2", "::3"];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv6Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        let (supernet, range) = ipv6_binary_split(&nets).unwrap();
        // ::2/127 in binary is "0b...10/0b...10", which contains both "::2" and "::3".
        assert_eq!(Ipv6Network::parse("::2/127").unwrap(), supernet);

        for (idx, net) in nets.iter().enumerate() {
            assert_eq!(range.contains(&idx), supernet.contains(net));
        }
    }

    #[test]
    fn test_ipv6_binary_split_more_nets() {
        let nets = &[
            "2a02:6b8:0:2a03::/64",
            "2a02:6b8:0:2a13::/64",
            "2a02:6b8:0:2a16::/64",
            "2a02:6b8:0:2a18::/64",
            "2a02:6b8:0:2a19::/64",
            "2a02:6b8:0:2a1c::/64",
            "2a02:6b8:0:2a1d::/64",
            "2a02:6b8:0:2a24::/64",
            "2a02:6b8:0:2800::/55",
        ];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv6Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        let (supernet, range) = ipv6_binary_split(&nets).unwrap();

        assert_eq!(Ipv6Network::parse("2a02:6b8:0:2a10::/60").unwrap(), supernet);

        for (idx, net) in nets.iter().enumerate() {
            assert_eq!(range.contains(&idx), supernet.contains(net));
        }
    }

    #[test]
    fn test_ipv6_binary_split_even_number_of_nets() {
        let nets = &[
            "2a02:6b8:0:2318::/64",
            "2a02:6b8:0:231e::/64",
            "2a02:6b8:0:231f::/64",
            "2a02:6b8:0:2302::/64",
            "2a02:6b8:0:2303::/64",
            "2a02:6b8:0:2305::/64",
            "2a02:6b8:0:2306::/64",
            "2a02:6b8:0:2307::/64",
            "2a02:6b8:0:2311::/64",
            "2a02:6b8:0:2314::/64",
        ];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv6Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        let (supernet, range) = ipv6_binary_split(&nets).unwrap();

        assert_eq!(Ipv6Network::parse("2a02:6b8:0:2300::/61").unwrap(), supernet);

        for (idx, net) in nets.iter().enumerate() {
            assert_eq!(range.contains(&idx), supernet.contains(net));
        }
    }

    #[test]
    fn test_ipv6_binary_split_in_the_middle() {
        let nets = &[
            "2a02:6b8:0:2302::/64",
            "2a02:6b8:0:2303::/64",
            "2a02:6b8:0:2305::/64",
            "2a02:6b8:0:2308::/64",
            "2a02:6b8:0:2309::/64",
            "2a02:6b8:0:230a::/64",
            "2a02:6b8:0:230b::/64",
            "2a02:6b8:0:230c::/64",
            "2a02:6b8:0:230d::/64",
            "2a02:6b8:0:2314::/64",
            "2a02:6b8:0:231f::/64",
        ];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv6Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        let (supernet, range) = ipv6_binary_split(&nets).unwrap();

        assert_eq!(Ipv6Network::parse("2a02:6b8:0:2308::/61").unwrap(), supernet);

        for (idx, net) in nets.iter().enumerate() {
            assert_eq!(range.contains(&idx), supernet.contains(net));
        }
    }

    #[test]
    fn test_ipv6_binary_split_non_contiguous() {
        let nets = &[
            "2a02:6b8:c00::4510:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::4511:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::4512:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::4513:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::4514:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::4515:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::4516:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::4517:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::4518:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::4519:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::451a:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
        ];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv6Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        let (supernet, range) = ipv6_binary_split(&nets).unwrap();

        assert_eq!(
            Ipv6Network::parse("2a02:6b8:c00::4510:0:0/ffff:ffff:ff00:0:ffff:fff8::").unwrap(),
            supernet
        );

        for (idx, net) in nets.iter().enumerate() {
            assert_eq!(range.contains(&idx), supernet.contains(net));
        }
    }

    #[test]
    fn test_ipv6_binary_split_non_contiguous_in_the_middle() {
        let nets = &[
            "2a02:6b8:c00::2302:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::2303:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::2305:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::2308:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::2309:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::230a:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::230b:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::230c:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::230d:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::2314:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
            "2a02:6b8:c00::231f:0:0/ffff:ffff:ff00::ffff:ffff:0:0",
        ];
        let mut nets: Vec<_> = nets.iter().map(|net| Ipv6Network::parse(net).unwrap()).collect();
        nets.sort();
        nets.dedup();

        let (supernet, range) = ipv6_binary_split(&nets).unwrap();

        assert_eq!(
            Ipv6Network::parse("2a02:6b8:c00::2308:0:0/ffff:ffff:ff00:0:ffff:fff8::").unwrap(),
            supernet
        );

        for (idx, net) in nets.iter().enumerate() {
            assert_eq!(range.contains(&idx), supernet.contains(net));
        }
    }

    #[test]
    fn test_ipnetwork_parse_v4() {
        let expected = IpNetwork::V4(Ipv4Network::new(
            Ipv4Addr::new(192, 168, 1, 0),
            Ipv4Addr::new(255, 255, 255, 0),
        ));

        assert_eq!(expected, IpNetwork::parse("192.168.1.0/24").unwrap());
    }

    #[test]
    fn test_ipnetwork_parse_v6() {
        let expected = IpNetwork::V6(Ipv6Network::new(
            Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0, 0, 0),
            Ipv6Addr::new(0xffff, 0xffff, 0xff00, 0, 0, 0, 0, 0),
        ));

        assert_eq!(expected, IpNetwork::parse("2a02:6b8:c00::/40").unwrap());
    }

    #[test]
    fn test_ipnetwork_parse_invalid() {
        assert_eq!(
            IpNetParseError::CidrOverflow(CidrOverflowError(33, 32)),
            IpNetwork::parse("192.168.1.0/33").unwrap_err(),
        );
    }

    #[test]
    fn test_ipnetwork_parse_invalid_v6() {
        assert_eq!(
            IpNetParseError::CidrOverflow(CidrOverflowError(129, 128)),
            IpNetwork::parse("2a02:6b8:c00::/129").unwrap_err(),
        );
    }

    #[test]
    fn test_contiguous_ip_network_parse_v4() {
        let expected = Contiguous(Ipv4Network::new(
            Ipv4Addr::new(192, 168, 1, 0),
            Ipv4Addr::new(255, 255, 255, 0),
        ));

        assert_eq!(expected, Contiguous::<Ipv4Network>::parse("192.168.1.0/24").unwrap(),);
    }

    #[test]
    fn test_contiguous_ip_network_parse_v4_invalid() {
        assert_eq!(
            ContiguousIpNetParseError::NonContiguousNetwork,
            Contiguous::<Ipv4Network>::parse("192.168.0.1/255.255.0.255").unwrap_err(),
        );
    }

    #[test]
    fn test_contiguous_ip_network_parse_v6() {
        let expected = Contiguous(Ipv6Network::new(
            Ipv6Addr::new(0x2a02, 0x6b8, 0xc00, 0, 0, 0, 0, 0),
            Ipv6Addr::new(0xffff, 0xffff, 0xff00, 0, 0, 0, 0, 0),
        ));

        assert_eq!(expected, Contiguous::<Ipv6Network>::parse("2a02:6b8:c00::/40").unwrap(),);
    }

    #[test]
    fn test_contiguous_ip_network_parse_v6_invalid() {
        assert_eq!(
            ContiguousIpNetParseError::NonContiguousNetwork,
            Contiguous::<Ipv6Network>::parse("2a02:6b8:c00::1234:0:0/ffff:ffff:ff00::ffff:ffff:0:0").unwrap_err(),
        );
    }
}
