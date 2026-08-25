use core::{
    error::Error,
    fmt::{self, Display, Formatter},
    net::{IpAddr, Ipv4Addr, Ipv6Addr},
    str::FromStr,
};

use netip::{Contiguous, IpNetwork, Ipv4Network, Ipv6Network, MacAddr, ipv4_range_to_networks, ipv6_range_to_networks};
use serde::{Deserialize, Deserializer, Serialize, Serializer, de};

#[allow(clippy::all, clippy::std_instead_of_core, non_snake_case)]
pub mod pb {
    tonic::include_proto!("common.commonpb.v1");
}

impl FromStr for pb::DevicePipeline {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let (name, weight) = s
            .split_once(':')
            .ok_or_else(|| format!("invalid pipeline format '{s}': expected 'name:weight'"))?;
        let weight = weight
            .parse::<u64>()
            .map_err(|e| format!("invalid weight in '{s}': {e}"))?;
        Ok(pb::DevicePipeline { name: name.to_string(), weight })
    }
}

impl From<IpAddr> for pb::IpAddress {
    fn from(addr: IpAddr) -> Self {
        let bytes = match addr {
            IpAddr::V4(v4) => v4.octets().to_vec(),
            IpAddr::V6(v6) => v6.octets().to_vec(),
        };
        pb::IpAddress { addr: bytes }
    }
}

impl From<MacAddr> for pb::MacAddress {
    fn from(mac: MacAddr) -> Self {
        pb::MacAddress { addr: mac.as_u64() }
    }
}

impl TryFrom<&pb::MacAddress> for MacAddr {
    type Error = Box<dyn Error>;

    fn try_from(mac: &pb::MacAddress) -> Result<Self, Self::Error> {
        if mac.addr >> 48 != 0 {
            return Err("upper 16 bits are set for MAC address".into());
        }

        Ok(MacAddr::from(mac.addr))
    }
}

impl FromStr for pb::MacAddress {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let mac = MacAddr::from_str(s)?;
        Ok(Self::from(mac))
    }
}

impl Display for pb::MacAddress {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match MacAddr::try_from(self) {
            Ok(mac) => mac.fmt(f),
            Err(..) => f.write_str("invalid"),
        }
    }
}

impl Serialize for pb::MacAddress {
    /// Serializes as the string `Display` renders.
    ///
    /// A message with the upper 16 bits set renders as the literal
    /// `"invalid"`, since that is what `Display` already falls back to.
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for pb::MacAddress {
    /// Parses the string `Serialize` produces, via `FromStr`.
    ///
    /// The literal `"invalid"` a set upper 16 bits serializes to is not
    /// itself a parseable MAC address, so it fails here with a
    /// deserialization error instead of reconstructing one -- the same
    /// deliberately lossy treatment `pb::IpAddress` gives its own
    /// malformed case.
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        s.parse::<Self>().map_err(de::Error::custom)
    }
}

impl TryFrom<&pb::IpAddress> for IpAddr {
    type Error = Box<dyn Error>;

    fn try_from(ip: &pb::IpAddress) -> Result<Self, Self::Error> {
        match ip.addr.len() {
            4 => {
                let octets: [u8; 4] = ip.addr[..].try_into().unwrap();
                Ok(IpAddr::V4(Ipv4Addr::from(octets)))
            }
            16 => {
                let octets: [u8; 16] = ip.addr[..].try_into().unwrap();
                Ok(IpAddr::V6(Ipv6Addr::from(octets)))
            }
            n => Err(format!("invalid IP address length {n}: expected 4 (IPv4) or 16 (IPv6)").into()),
        }
    }
}

impl Display for pb::IpAddress {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match IpAddr::try_from(self) {
            Ok(IpAddr::V6(v6)) => match v6.to_ipv4_mapped() {
                Some(v4) => v4.fmt(f),
                None => v6.fmt(f),
            },
            Ok(addr) => addr.fmt(f),
            Err(..) => f.write_str("invalid"),
        }
    }
}

impl FromStr for pb::IpAddress {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let addr = IpAddr::from_str(s)?;
        Ok(Self::from(addr))
    }
}

impl Serialize for pb::IpAddress {
    /// Serializes as the plain address string `Display` renders.
    ///
    /// A malformed byte length renders as the literal `"invalid"`, since
    /// that is what `Display` already falls back to. An IPv4-mapped IPv6
    /// address renders as its unmapped 4-byte form too, so the round trip
    /// through this string is lossy for that one input shape as well.
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for pb::IpAddress {
    /// Parses the string `Serialize` produces, via `FromStr`.
    ///
    /// The literal `"invalid"` a malformed byte length serializes to is
    /// not itself a parseable address, so it fails here with a
    /// deserialization error instead of reconstructing some address for
    /// it. A malformed `IpAddress` is therefore lossy across this string
    /// encoding, deliberately, rather than round-tripping.
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        s.parse::<Self>().map_err(de::Error::custom)
    }
}

impl From<Ipv4Addr> for pb::IPv4Address {
    fn from(addr: Ipv4Addr) -> Self {
        pb::IPv4Address { addr: addr.to_bits() }
    }
}

impl From<&pb::IPv4Address> for Ipv4Addr {
    fn from(ip: &pb::IPv4Address) -> Self {
        Ipv4Addr::from_bits(ip.addr)
    }
}

impl Display for pb::IPv4Address {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        Ipv4Addr::from(self).fmt(f)
    }
}

impl FromStr for pb::IPv4Address {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let addr = Ipv4Addr::from_str(s)?;
        Ok(Self::from(addr))
    }
}

impl Serialize for pb::IPv4Address {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for pb::IPv4Address {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        s.parse::<Self>().map_err(de::Error::custom)
    }
}

impl From<Ipv6Addr> for pb::IPv6Address {
    fn from(addr: Ipv6Addr) -> Self {
        let bits = addr.to_bits();
        pb::IPv6Address {
            hi: (bits >> 64) as u64,
            lo: bits as u64,
        }
    }
}

impl From<&pb::IPv6Address> for Ipv6Addr {
    fn from(ip: &pb::IPv6Address) -> Self {
        Ipv6Addr::from_bits((u128::from(ip.hi) << 64) | u128::from(ip.lo))
    }
}

impl Display for pb::IPv6Address {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        Ipv6Addr::from(self).fmt(f)
    }
}

impl FromStr for pb::IPv6Address {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let addr = Ipv6Addr::from_str(s)?;
        Ok(Self::from(addr))
    }
}

impl Serialize for pb::IPv6Address {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for pb::IPv6Address {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        s.parse::<Self>().map_err(de::Error::custom)
    }
}

impl From<Contiguous<Ipv4Network>> for pb::IPv4Prefix {
    fn from(net: Contiguous<Ipv4Network>) -> Self {
        pb::IPv4Prefix {
            addr: Some(pb::IPv4Address::from(*net.addr())),
            prefix_len: u32::from(net.prefix()),
        }
    }
}

impl TryFrom<&pb::IPv4Prefix> for Contiguous<Ipv4Network> {
    type Error = Box<dyn Error>;

    /// Masks host bits to `prefix_len` rather than rejecting them.
    fn try_from(net: &pb::IPv4Prefix) -> Result<Self, Self::Error> {
        let addr = net.addr.as_ref().ok_or("invalid IP network: missing address")?;
        let addr = Ipv4Addr::from(addr);
        let prefix_len =
            u8::try_from(net.prefix_len).map_err(|_| format!("invalid prefix length {}", net.prefix_len))?;
        Contiguous::<Ipv4Network>::try_from((addr, prefix_len))
            .map_err(|e| format!("invalid prefix length {prefix_len}: {e}").into())
    }
}

impl Display for pb::IPv4Prefix {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match Contiguous::<Ipv4Network>::try_from(self) {
            Ok(net) => net.fmt(f),
            Err(..) => f.write_str("invalid"),
        }
    }
}

impl FromStr for pb::IPv4Prefix {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let net = Contiguous::<Ipv4Network>::parse(s)?;
        Ok(Self::from(net))
    }
}

impl Serialize for pb::IPv4Prefix {
    /// Serializes as the CIDR string `Display` renders.
    ///
    /// A message with an absent address renders as the literal
    /// `"invalid"`, which is not itself a parseable network and so
    /// deliberately does not deserialize back.
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for pb::IPv4Prefix {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        s.parse::<Self>().map_err(de::Error::custom)
    }
}

impl From<Ipv4Network> for pb::IPv4Network {
    fn from(net: Ipv4Network) -> Self {
        pb::IPv4Network {
            addr: Some(pb::IPv4Address::from(*net.addr())),
            mask: Some(pb::IPv4Address::from(*net.mask())),
        }
    }
}

impl TryFrom<&pb::IPv4Network> for Ipv4Network {
    type Error = Box<dyn Error>;

    fn try_from(net: &pb::IPv4Network) -> Result<Self, Self::Error> {
        let addr = net.addr.as_ref().ok_or("invalid IP network: missing address")?;
        let mask = net.mask.as_ref().ok_or("invalid IP network: missing mask")?;
        Ok(Ipv4Network::new(Ipv4Addr::from(addr), Ipv4Addr::from(mask)))
    }
}

impl Display for pb::IPv4Network {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match Ipv4Network::try_from(self) {
            Ok(net) => net.fmt(f),
            Err(..) => f.write_str("invalid"),
        }
    }
}

impl FromStr for pb::IPv4Network {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let net = Ipv4Network::parse(s)?;
        Ok(Self::from(net))
    }
}

impl Serialize for pb::IPv4Network {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for pb::IPv4Network {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        s.parse::<Self>().map_err(de::Error::custom)
    }
}

impl From<Contiguous<Ipv6Network>> for pb::IPv6Prefix {
    fn from(net: Contiguous<Ipv6Network>) -> Self {
        pb::IPv6Prefix {
            addr: Some(pb::IPv6Address::from(*net.addr())),
            prefix_len: u32::from(net.prefix()),
        }
    }
}

impl TryFrom<&pb::IPv6Prefix> for Contiguous<Ipv6Network> {
    type Error = Box<dyn Error>;

    /// Masks host bits to `prefix_len` rather than rejecting them.
    fn try_from(net: &pb::IPv6Prefix) -> Result<Self, Self::Error> {
        let addr = net.addr.as_ref().ok_or("invalid IP network: missing address")?;
        let addr = Ipv6Addr::from(addr);
        let prefix_len =
            u8::try_from(net.prefix_len).map_err(|_| format!("invalid prefix length {}", net.prefix_len))?;
        Contiguous::<Ipv6Network>::try_from((addr, prefix_len))
            .map_err(|e| format!("invalid prefix length {prefix_len}: {e}").into())
    }
}

impl Display for pb::IPv6Prefix {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match Contiguous::<Ipv6Network>::try_from(self) {
            Ok(net) => net.fmt(f),
            Err(..) => f.write_str("invalid"),
        }
    }
}

impl FromStr for pb::IPv6Prefix {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let net = Contiguous::<Ipv6Network>::parse(s)?;
        Ok(Self::from(net))
    }
}

impl Serialize for pb::IPv6Prefix {
    /// Serializes as the CIDR string `Display` renders.
    ///
    /// A message with an absent address renders as the literal
    /// `"invalid"`, which is not itself a parseable network and so
    /// deliberately does not deserialize back.
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for pb::IPv6Prefix {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        s.parse::<Self>().map_err(de::Error::custom)
    }
}

impl From<Ipv6Network> for pb::IPv6Network {
    fn from(net: Ipv6Network) -> Self {
        pb::IPv6Network {
            addr: Some(pb::IPv6Address::from(*net.addr())),
            mask: Some(pb::IPv6Address::from(*net.mask())),
        }
    }
}

impl TryFrom<&pb::IPv6Network> for Ipv6Network {
    type Error = Box<dyn Error>;

    fn try_from(net: &pb::IPv6Network) -> Result<Self, Self::Error> {
        let addr = net.addr.as_ref().ok_or("invalid IP network: missing address")?;
        let mask = net.mask.as_ref().ok_or("invalid IP network: missing mask")?;
        Ok(Ipv6Network::new(Ipv6Addr::from(addr), Ipv6Addr::from(mask)))
    }
}

impl Display for pb::IPv6Network {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match Ipv6Network::try_from(self) {
            Ok(net) => net.fmt(f),
            Err(..) => f.write_str("invalid"),
        }
    }
}

impl FromStr for pb::IPv6Network {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let net = Ipv6Network::parse(s)?;
        Ok(Self::from(net))
    }
}

impl Serialize for pb::IPv6Network {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for pb::IPv6Network {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        s.parse::<Self>().map_err(de::Error::custom)
    }
}

impl From<(IpAddr, IpAddr)> for pb::IpRange {
    fn from((start, end): (IpAddr, IpAddr)) -> Self {
        pb::IpRange {
            start: Some(pb::IpAddress::from(start)),
            end: Some(pb::IpAddress::from(end)),
        }
    }
}

impl TryFrom<&pb::IpRange> for (IpAddr, IpAddr) {
    type Error = Box<dyn Error>;

    fn try_from(range: &pb::IpRange) -> Result<Self, Self::Error> {
        let start = range.start.as_ref().ok_or("invalid IP range: missing start address")?;
        let end = range.end.as_ref().ok_or("invalid IP range: missing end address")?;
        let start = IpAddr::try_from(start)?;
        let end = IpAddr::try_from(end)?;
        if start.is_ipv4() != end.is_ipv4() {
            return Err("invalid IP range: address family mismatch between start and end".into());
        }

        Ok((start, end))
    }
}

impl Display for pb::IpRange {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match <(IpAddr, IpAddr)>::try_from(self) {
            Ok((start, end)) => write!(f, "[{start}, {end}]"),
            Err(..) => f.write_str("invalid"),
        }
    }
}

impl pb::IpRange {
    /// Returns an iterator over the minimum set of CIDR blocks covering the
    /// range.
    ///
    /// Each item is a `Contiguous<IpNetwork>` carrying the guarantee that the
    /// prefix fits a contiguous slice of `[start, end]` — no non-contiguous
    /// mask bits. On any conversion error (missing endpoint, family mismatch),
    /// returns an empty iterator without panicking.
    pub fn cidrs(&self) -> Box<dyn Iterator<Item = Contiguous<IpNetwork>> + '_> {
        let (start, end) = match <(IpAddr, IpAddr)>::try_from(self) {
            Ok(pair) => pair,
            Err(..) => return Box::new(core::iter::empty()),
        };

        match (start, end) {
            (IpAddr::V4(start), IpAddr::V4(end)) => {
                Box::new(ipv4_range_to_networks(start, end).map(Contiguous::<IpNetwork>::from))
            }
            (IpAddr::V6(start), IpAddr::V6(end)) => {
                Box::new(ipv6_range_to_networks(start, end).map(Contiguous::<IpNetwork>::from))
            }
            _ => Box::new(core::iter::empty()),
        }
    }
}

impl From<Contiguous<IpNetwork>> for pb::IpPrefix {
    fn from(net: Contiguous<IpNetwork>) -> Self {
        pb::IpPrefix {
            addr: Some(pb::IpAddress::from(net.addr())),
            prefix_len: u32::from(net.prefix()),
        }
    }
}

impl TryFrom<IpNetwork> for pb::IpPrefix {
    type Error = Box<dyn Error>;

    /// Fails when `net`'s mask is not expressible as a prefix length --
    /// that contiguity is the whole point of this message.
    fn try_from(net: IpNetwork) -> Result<Self, Self::Error> {
        let prefix = net.prefix().ok_or("invalid IP network: mask is not contiguous")?;
        Ok(pb::IpPrefix {
            addr: Some(pb::IpAddress::from(net.addr())),
            prefix_len: u32::from(prefix),
        })
    }
}

/// Decodes the address and prefix length a `IpPrefix` carries.
///
/// Shared by both network conversions below so the wire validation lives in
/// one place and the two decode paths cannot drift apart.
fn decode_ip_network(net: &pb::IpPrefix) -> Result<(IpAddr, u8), Box<dyn Error>> {
    let addr = net.addr.as_ref().ok_or("invalid IP network: missing address")?;
    let addr = IpAddr::try_from(addr)?;
    let prefix_len: u8 = net
        .prefix_len
        .try_into()
        .map_err(|_| format!("invalid prefix length {}: exceeds 255", net.prefix_len))?;

    Ok((addr, prefix_len))
}

impl TryFrom<&pb::IpPrefix> for IpNetwork {
    type Error = Box<dyn Error>;

    /// Masks host bits to `prefix_len` rather than rejecting them.
    fn try_from(net: &pb::IpPrefix) -> Result<Self, Self::Error> {
        let (addr, prefix_len) = decode_ip_network(net)?;

        let result = match addr {
            IpAddr::V4(v4) => IpNetwork::try_from((v4, prefix_len)),
            IpAddr::V6(v6) => IpNetwork::try_from((v6, prefix_len)),
        };
        result.map_err(|e| format!("invalid prefix length {prefix_len}: {e}").into())
    }
}

impl TryFrom<&pb::IpPrefix> for Contiguous<IpNetwork> {
    type Error = Box<dyn Error>;

    /// Masks host bits to `prefix_len`, like the [`IpNetwork`] conversion.
    ///
    /// A mask built from a prefix length is contiguous by construction, so
    /// this never has to reject a non-contiguous mask.
    fn try_from(net: &pb::IpPrefix) -> Result<Self, Self::Error> {
        let (addr, prefix_len) = decode_ip_network(net)?;

        Contiguous::<IpNetwork>::try_from((addr, prefix_len))
            .map_err(|e| format!("invalid prefix length {prefix_len}: {e}").into())
    }
}

impl Display for pb::IpPrefix {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match IpNetwork::try_from(self) {
            Ok(net) => net.fmt(f),
            Err(..) => f.write_str("invalid"),
        }
    }
}

impl FromStr for pb::IpPrefix {
    type Err = Box<dyn Error>;

    /// Accepts CIDR (`10.0.0.0/24`), explicit-mask (`10.0.0.0/255.255.255.0`),
    /// and bare-address (`10.0.0.1`) forms -- the last silently promoted to
    /// a `/32` or `/128` host route -- and rejects a non-contiguous mask.
    /// Wider than the Go side, which decodes only the CIDR form.
    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let net = Contiguous::<IpNetwork>::parse(s)?;
        Ok(Self::from(net))
    }
}

impl Serialize for pb::IpPrefix {
    /// Serializes as the CIDR string `Display` renders.
    ///
    /// A malformed message renders as the literal `"invalid"`, since that
    /// is what `Display` already falls back to. That literal is not itself
    /// a parseable network, so it deliberately does not deserialize back --
    /// the same lossy treatment `pb::IpAddress` gives its own malformed
    /// case.
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for pb::IpPrefix {
    /// Parses the string `Serialize` produces, via `FromStr`.
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        s.parse::<Self>().map_err(de::Error::custom)
    }
}

/// Partitions mixed-family contiguous prefixes into the family-typed
/// prefix messages, preserving the within-family order.
pub fn partition_prefixes(
    prefixes: impl IntoIterator<Item = Contiguous<IpNetwork>>,
) -> (Vec<pb::IPv4Prefix>, Vec<pb::IPv6Prefix>) {
    let mut v4 = Vec::new();
    let mut v6 = Vec::new();
    for prefix in prefixes {
        match prefix.addr() {
            IpAddr::V4(addr) => v4.push(pb::IPv4Prefix {
                addr: Some(pb::IPv4Address::from(addr)),
                prefix_len: u32::from(prefix.prefix()),
            }),
            IpAddr::V6(addr) => v6.push(pb::IPv6Prefix {
                addr: Some(pb::IPv6Address::from(addr)),
                prefix_len: u32::from(prefix.prefix()),
            }),
        }
    }

    (v4, v6)
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn partition_prefixes_splits_by_family_preserving_order() {
        let prefixes = ["2001:db8::/32", "10.0.0.0/8", "192.0.2.0/24", "2001:db8:1::/48"]
            .map(|prefix| Contiguous::<IpNetwork>::parse(prefix).unwrap());

        let (v4, v6) = partition_prefixes(prefixes);

        assert_eq!(
            vec!["10.0.0.0/8", "192.0.2.0/24"],
            v4.iter().map(ToString::to_string).collect::<Vec<_>>()
        );
        assert_eq!(
            vec!["2001:db8::/32", "2001:db8:1::/48"],
            v6.iter().map(ToString::to_string).collect::<Vec<_>>()
        );
    }

    #[test]
    fn v4_round_trip() {
        let addr = IpAddr::V4(Ipv4Addr::new(192, 168, 1, 1));
        let ip = pb::IpAddress::from(addr);
        assert_eq!(4, ip.addr.len());
        let got = IpAddr::try_from(&ip).unwrap();
        assert_eq!(addr, got);
    }

    #[test]
    fn v6_round_trip() {
        let addr = IpAddr::V6(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1));
        let ip = pb::IpAddress::from(addr);
        assert_eq!(16, ip.addr.len());
        let got = IpAddr::try_from(&ip).unwrap();
        assert_eq!(addr, got);
    }

    #[test]
    fn try_from_rejects_invalid_lengths() {
        for len in [0usize, 1, 3, 5, 15, 17] {
            let ip = pb::IpAddress { addr: vec![0u8; len] };
            assert!(IpAddr::try_from(&ip).is_err(), "expected error for length {len}");
        }
    }

    #[test]
    fn from_str_parses_valid() {
        let v4: pb::IpAddress = "10.0.0.1".parse().unwrap();
        assert_eq!(vec![10, 0, 0, 1], v4.addr);

        let v6: pb::IpAddress = "2001:db8::1".parse().unwrap();
        let got = IpAddr::try_from(&v6).unwrap();
        assert_eq!(IpAddr::V6(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1)), got);
    }

    #[test]
    fn from_str_rejects_invalid() {
        assert!("".parse::<pb::IpAddress>().is_err());
        assert!("not-an-ip".parse::<pb::IpAddress>().is_err());
    }

    #[test]
    fn display_v4() {
        let ip = pb::IpAddress::from(IpAddr::V4(Ipv4Addr::new(10, 0, 0, 1)));
        assert_eq!("10.0.0.1", ip.to_string());
    }

    #[test]
    fn display_v6() {
        let ip = pb::IpAddress::from(IpAddr::V6(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1)));
        assert_eq!("2001:db8::1", ip.to_string());
    }

    #[test]
    fn display_invalid_length() {
        let ip = pb::IpAddress { addr: vec![0u8; 5] };
        assert_eq!("invalid", ip.to_string());
    }

    #[test]
    fn ip_address_display_unwraps_ipv4_mapped() {
        let ip = pb::IpAddress::from(IpAddr::V6(Ipv4Addr::new(141, 8, 128, 254).to_ipv6_mapped()));
        assert_eq!("141.8.128.254", ip.to_string());
    }

    #[test]
    fn display_v6_unspecified() {
        let ip = pb::IpAddress::from(IpAddr::V6(Ipv6Addr::UNSPECIFIED));
        assert_eq!("::", ip.to_string());
    }

    #[test]
    fn iprange_v4_round_trip() {
        let start = IpAddr::V4(Ipv4Addr::new(10, 0, 0, 0));
        let end = IpAddr::V4(Ipv4Addr::new(10, 0, 0, 255));
        let range = pb::IpRange::from((start, end));
        let (got_start, got_end) = <(IpAddr, IpAddr)>::try_from(&range).unwrap();
        assert_eq!(start, got_start);
        assert_eq!(end, got_end);
    }

    #[test]
    fn iprange_v6_round_trip() {
        let start = IpAddr::V6(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 0));
        let end = IpAddr::V6(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1));
        let range = pb::IpRange::from((start, end));
        let (got_start, got_end) = <(IpAddr, IpAddr)>::try_from(&range).unwrap();
        assert_eq!(start, got_start);
        assert_eq!(end, got_end);
    }

    #[test]
    fn iprange_try_from_rejects_family_mismatch() {
        let start = IpAddr::V4(Ipv4Addr::new(10, 0, 0, 1));
        let end = IpAddr::V6(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1));
        let range = pb::IpRange {
            start: Some(pb::IpAddress::from(start)),
            end: Some(pb::IpAddress::from(end)),
        };
        assert!(<(IpAddr, IpAddr)>::try_from(&range).is_err());
    }

    #[test]
    fn iprange_display_v4() {
        let start = IpAddr::V4(Ipv4Addr::new(10, 0, 0, 0));
        let end = IpAddr::V4(Ipv4Addr::new(10, 0, 0, 255));
        let range = pb::IpRange::from((start, end));
        assert_eq!("[10.0.0.0, 10.0.0.255]", range.to_string());
    }

    #[test]
    fn iprange_display_v6() {
        let start = IpAddr::V6(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 0));
        let end = IpAddr::V6(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1));
        let range = pb::IpRange::from((start, end));
        assert_eq!("[2001:db8::, 2001:db8::1]", range.to_string());
    }

    #[test]
    fn iprange_display_invalid() {
        let range = pb::IpRange { start: None, end: None };
        assert_eq!("invalid", range.to_string());
    }

    #[test]
    fn mac_round_trip() {
        let mac = "aa:bb:cc:dd:ee:ff".parse::<MacAddr>().unwrap();
        let proto = pb::MacAddress::from(mac);
        let got = MacAddr::try_from(&proto).unwrap();
        assert_eq!(mac, got);
    }

    #[test]
    fn mac_try_from_rejects_upper_bits() {
        let proto = pb::MacAddress { addr: 0x1_0000_0000_0000 };
        assert!(MacAddr::try_from(&proto).is_err());
    }

    #[test]
    fn mac_from_str_parses_valid() {
        let mac: pb::MacAddress = "aa:bb:cc:dd:ee:ff".parse().unwrap();
        assert_eq!(0xaabbccddeeff, mac.addr);
    }

    #[test]
    fn mac_from_str_rejects_invalid() {
        assert!("".parse::<pb::MacAddress>().is_err());
        assert!("not-a-mac".parse::<pb::MacAddress>().is_err());
    }

    #[test]
    fn serde_ip_address_v4() {
        let ip = pb::IpAddress::from(IpAddr::V4(Ipv4Addr::new(10, 0, 0, 1)));
        let json = serde_json::to_string(&ip).unwrap();
        assert_eq!(r#""10.0.0.1""#, json);
        let got: pb::IpAddress = serde_json::from_str(&json).unwrap();
        assert_eq!(ip, got);
    }

    #[test]
    fn serde_ip_address_v6() {
        let ip = pb::IpAddress::from(IpAddr::V6(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1)));
        let json = serde_json::to_string(&ip).unwrap();
        assert_eq!(r#""2001:db8::1""#, json);
        let got: pb::IpAddress = serde_json::from_str(&json).unwrap();
        assert_eq!(ip, got);
    }

    /// An IPv4-mapped IPv6 address's `Display` unmaps it, so its string
    /// form serializes and deserializes as a plain 4-byte IPv4 address --
    /// not the original 16-byte mapped wire form. The round trip is
    /// therefore family-normalizing, not byte-preserving, for this one
    /// input shape.
    #[test]
    fn serde_ip_address_v4_mapped_does_not_round_trip_bytes() {
        let mapped = pb::IpAddress::from(IpAddr::V6(Ipv4Addr::new(141, 8, 128, 254).to_ipv6_mapped()));
        assert_eq!(16, mapped.addr.len());

        let json = serde_json::to_string(&mapped).unwrap();
        assert_eq!(r#""141.8.128.254""#, json);

        let got: pb::IpAddress = serde_json::from_str(&json).unwrap();
        assert_eq!(4, got.addr.len());
        assert_ne!(mapped, got);
        assert_eq!(pb::IpAddress::from(IpAddr::V4(Ipv4Addr::new(141, 8, 128, 254))), got);
    }

    /// A malformed byte length serializes to the same `"invalid"` literal
    /// `Display` already falls back to, but that literal is not itself a
    /// parseable address -- deserializing it back is a deliberate error,
    /// not a silently reconstructed address.
    #[test]
    fn serde_ip_address_malformed_length_serializes_but_does_not_deserialize() {
        let malformed = pb::IpAddress { addr: vec![0u8; 5] };
        let json = serde_json::to_string(&malformed).unwrap();
        assert_eq!(r#""invalid""#, json);
        assert!(serde_json::from_str::<pb::IpAddress>(&json).is_err());
    }

    #[test]
    fn mac_display() {
        let mac = pb::MacAddress::from("aa:bb:cc:dd:ee:ff".parse::<MacAddr>().unwrap());
        assert_eq!("aa:bb:cc:dd:ee:ff", mac.to_string());
    }

    #[test]
    fn mac_display_invalid_upper_bits() {
        let mac = pb::MacAddress { addr: 0x1_0000_0000_0000 };
        assert_eq!("invalid", mac.to_string());
    }

    #[test]
    fn mac_display_default() {
        assert_eq!("00:00:00:00:00:00", pb::MacAddress::default().to_string());
    }

    #[test]
    fn serde_mac_address() {
        let mac = pb::MacAddress::from("aa:bb:cc:dd:ee:ff".parse::<MacAddr>().unwrap());
        let json = serde_json::to_string(&mac).unwrap();
        assert_eq!(r#""aa:bb:cc:dd:ee:ff""#, json);
        let got: pb::MacAddress = serde_json::from_str(&json).unwrap();
        assert_eq!(mac, got);
    }

    /// A message with the upper 16 bits set serializes to `"invalid"`, the
    /// same fallback [`mac_try_from_rejects_upper_bits`] proves `MacAddr`
    /// itself rejects, and that literal does not deserialize back.
    #[test]
    fn serde_mac_address_upper_bits_serializes_but_does_not_deserialize() {
        let malformed = pb::MacAddress { addr: 0x1_0000_0000_0000 };
        let json = serde_json::to_string(&malformed).unwrap();
        assert_eq!(r#""invalid""#, json);
        assert!(serde_json::from_str::<pb::MacAddress>(&json).is_err());
    }

    /// `IpRange`'s derive needs no hand-written impl of its own: once its
    /// two `IPAddress` fields serialize as strings, the derived shape is
    /// already the nested `{"start": "...", "end": "..."}` form.
    #[test]
    fn serde_ip_range_nested_shape() {
        let start = IpAddr::V4(Ipv4Addr::new(10, 0, 0, 0));
        let end = IpAddr::V4(Ipv4Addr::new(10, 0, 0, 255));
        let range = pb::IpRange::from((start, end));

        let json = serde_json::to_string(&range).unwrap();
        assert_eq!(r#"{"start":"10.0.0.0","end":"10.0.0.255"}"#, json);

        let got: pb::IpRange = serde_json::from_str(&json).unwrap();
        assert_eq!(range, got);
    }

    #[test]
    fn iprange_cidrs_v4() {
        let start = IpAddr::V4(Ipv4Addr::new(10, 0, 0, 0));
        let end = IpAddr::V4(Ipv4Addr::new(10, 0, 0, 5));
        let range = pb::IpRange::from((start, end));
        let cidrs: Vec<String> = range.cidrs().map(|net| net.to_string()).collect();
        assert_eq!(vec!["10.0.0.0/30", "10.0.0.4/31"], cidrs);
    }

    #[test]
    fn iprange_cidrs_single() {
        let addr = IpAddr::V4(Ipv4Addr::new(192, 168, 1, 1));
        let range = pb::IpRange::from((addr, addr));
        let cidrs: Vec<String> = range.cidrs().map(|net| net.to_string()).collect();
        assert_eq!(1, cidrs.len());
        assert_eq!("192.168.1.1/32", cidrs[0]);
    }

    /// The `value` oneof's wire name (`counter`), not the Rust variant
    /// identifier (`Counter`) prost generates for it, is what must appear
    /// in the tag.
    #[test]
    fn serde_metric_value_oneof_uses_snake_case_tag() {
        let metric = pb::Metric {
            name: "fwstate_sync_packets".to_string(),
            labels: vec![],
            value: Some(pb::metric::Value::Counter(42)),
        };
        let json = serde_json::to_string(&metric).unwrap();
        assert_eq!(
            r#"{"name":"fwstate_sync_packets","labels":[],"value":{"counter":42}}"#,
            json
        );
    }

    #[test]
    fn iprange_cidrs_invalid_family() {
        let start = IpAddr::V4(Ipv4Addr::new(10, 0, 0, 1));
        let end = IpAddr::V6(Ipv6Addr::new(0x2001, 0xdb8, 0, 0, 0, 0, 0, 1));
        let range = pb::IpRange {
            start: Some(pb::IpAddress::from(start)),
            end: Some(pb::IpAddress::from(end)),
        };
        let cidrs: Vec<Contiguous<IpNetwork>> = range.cidrs().collect();
        assert_eq!(0, cidrs.len());
    }

    #[test]
    fn contiguous_ip_network_v4_round_trip() {
        let net = Contiguous::<IpNetwork>::parse("10.0.0.0/24").unwrap();
        let msg = pb::IpPrefix::from(net);
        assert_eq!(24, msg.prefix_len);
        let got = IpNetwork::try_from(&msg).unwrap();
        assert_eq!(*net, got);
    }

    #[test]
    fn contiguous_ip_network_v6_round_trip() {
        let net = Contiguous::<IpNetwork>::parse("2001:db8::/32").unwrap();
        let msg = pb::IpPrefix::from(net);
        assert_eq!(32, msg.prefix_len);
        let got = IpNetwork::try_from(&msg).unwrap();
        assert_eq!(*net, got);
    }

    /// The `Contiguous<IpNetwork>` decode accepts exactly what the bare
    /// `IpNetwork` one does, since both go through `decode_ip_network`, and
    /// keeps the contiguity guarantee in the type.
    #[test]
    fn contiguous_ip_network_contiguous_round_trip() {
        for cidr in ["10.0.0.0/24", "2001:db8::/32"] {
            let net = Contiguous::<IpNetwork>::parse(cidr).unwrap();
            let msg = pb::IpPrefix::from(net);
            let got = Contiguous::<IpNetwork>::try_from(&msg).unwrap();
            assert_eq!(net, got, "round trip must preserve {cidr}");
        }
    }

    /// Mirrors [`contiguous_ip_network_try_from_masks_host_bits_v4`] for the
    /// `Contiguous<IpNetwork>` decode.
    #[test]
    fn contiguous_ip_network_contiguous_try_from_masks_host_bits() {
        let msg = pb::IpPrefix {
            addr: Some(pb::IpAddress::from(IpAddr::V4(Ipv4Addr::new(10, 0, 0, 1)))),
            prefix_len: 24,
        };
        let got = Contiguous::<IpNetwork>::try_from(&msg).unwrap();
        assert_eq!(Contiguous::<IpNetwork>::parse("10.0.0.0/24").unwrap(), got);
    }

    /// Both decodes reject the same malformed messages, since the shared
    /// `decode_ip_network` is what rejects them.
    #[test]
    fn contiguous_ip_network_contiguous_try_from_rejects_malformed() {
        let malformed = [
            pb::IpPrefix { addr: None, prefix_len: 24 },
            pb::IpPrefix {
                addr: Some(pb::IpAddress { addr: vec![10, 0, 0] }),
                prefix_len: 24,
            },
            pb::IpPrefix {
                addr: Some(pb::IpAddress::from(IpAddr::V4(Ipv4Addr::new(10, 0, 0, 0)))),
                prefix_len: 33,
            },
        ];

        for msg in &malformed {
            assert!(Contiguous::<IpNetwork>::try_from(msg).is_err(), "must reject {msg:?}");
            assert!(IpNetwork::try_from(msg).is_err(), "must reject {msg:?}");
        }
    }

    /// `Display` alone would hide a wrong-family or unmasked encoding, so
    /// this asserts on the raw wire bytes of `addr` instead.
    #[test]
    fn contiguous_ip_network_masks_host_bits() {
        let msg: pb::IpPrefix = "10.0.0.1/24".parse().unwrap();
        assert_eq!(vec![10, 0, 0, 0], msg.addr.as_ref().unwrap().addr);
        assert_eq!(24, msg.prefix_len);
    }

    /// Mirrors [`contiguous_ip_network_masks_host_bits`] on the decode side:
    /// a hand-built message with host bits already set in `addr` still masks
    /// down to the network base rather than merely echoing them back.
    #[test]
    fn contiguous_ip_network_try_from_masks_host_bits_v4() {
        let msg = pb::IpPrefix {
            addr: Some(pb::IpAddress::from(IpAddr::V4(Ipv4Addr::new(10, 0, 0, 1)))),
            prefix_len: 24,
        };
        let got = IpNetwork::try_from(&msg).unwrap();
        assert_eq!(IpNetwork::parse("10.0.0.0/24").unwrap(), got);
    }

    #[test]
    fn contiguous_ip_network_try_from_masks_host_bits_v6() {
        let msg = pb::IpPrefix {
            addr: Some(pb::IpAddress::from(IpAddr::V6(Ipv6Addr::new(
                0x2001, 0xdb8, 0, 0, 0, 0, 0, 1,
            )))),
            prefix_len: 32,
        };
        let got = IpNetwork::try_from(&msg).unwrap();
        assert_eq!(IpNetwork::parse("2001:db8::/32").unwrap(), got);
    }

    #[test]
    fn contiguous_ip_network_try_from_rejects_non_contiguous_mask() {
        let net = IpNetwork::parse("192.168.0.1/255.255.0.255").unwrap();
        assert!(pb::IpPrefix::try_from(net).is_err());
    }

    #[test]
    fn contiguous_ip_network_try_from_accepts_contiguous_mask() {
        let net = IpNetwork::parse("192.168.0.0/255.255.255.0").unwrap();
        let msg = pb::IpPrefix::try_from(net).unwrap();
        assert_eq!(24, msg.prefix_len);
    }

    #[test]
    fn contiguous_ip_network_rejects_missing_addr() {
        let msg = pb::IpPrefix { addr: None, prefix_len: 24 };
        assert!(IpNetwork::try_from(&msg).is_err());
    }

    #[test]
    fn contiguous_ip_network_rejects_malformed_addr_length() {
        let msg = pb::IpPrefix {
            addr: Some(pb::IpAddress { addr: vec![0u8; 5] }),
            prefix_len: 8,
        };
        assert!(IpNetwork::try_from(&msg).is_err());
    }

    #[test]
    fn contiguous_ip_network_rejects_out_of_range_prefix_len_v4() {
        let msg = pb::IpPrefix {
            addr: Some(pb::IpAddress::from(IpAddr::V4(Ipv4Addr::new(10, 0, 0, 0)))),
            prefix_len: 33,
        };
        assert!(IpNetwork::try_from(&msg).is_err());
    }

    #[test]
    fn contiguous_ip_network_rejects_out_of_range_prefix_len_v6() {
        let msg = pb::IpPrefix {
            addr: Some(pb::IpAddress::from(IpAddr::V6(Ipv6Addr::new(
                0x2001, 0xdb8, 0, 0, 0, 0, 0, 0,
            )))),
            prefix_len: 129,
        };
        assert!(IpNetwork::try_from(&msg).is_err());
    }

    /// `256` and `288` truncate to a valid `u8` (`0` and `32`), so a
    /// checked narrowing is needed -- an unchecked `as u8` would silently
    /// decode `256` as `/0` and `288` as `/32` instead of rejecting them.
    #[test]
    fn contiguous_ip_network_rejects_prefix_len_that_truncates_into_range() {
        let addr = Some(pb::IpAddress::from(IpAddr::V4(Ipv4Addr::new(10, 0, 0, 0))));
        for prefix_len in [256, 288] {
            let msg = pb::IpPrefix { addr: addr.clone(), prefix_len };
            assert!(
                IpNetwork::try_from(&msg).is_err(),
                "expected error for prefix_len {prefix_len}"
            );
        }
    }

    #[test]
    fn contiguous_ip_network_display() {
        let net = Contiguous::<IpNetwork>::parse("10.0.0.0/24").unwrap();
        let msg = pb::IpPrefix::from(net);
        assert_eq!("10.0.0.0/24", msg.to_string());
    }

    #[test]
    fn contiguous_ip_network_display_invalid() {
        let msg = pb::IpPrefix { addr: None, prefix_len: 24 };
        assert_eq!("invalid", msg.to_string());
    }

    #[test]
    fn serde_contiguous_ip_network_v4() {
        let net = Contiguous::<IpNetwork>::parse("10.0.0.0/24").unwrap();
        let msg = pb::IpPrefix::from(net);
        let json = serde_json::to_string(&msg).unwrap();
        assert_eq!(r#""10.0.0.0/24""#, json);
        let got: pb::IpPrefix = serde_json::from_str(&json).unwrap();
        assert_eq!(msg, got);
    }

    #[test]
    fn serde_contiguous_ip_network_v6() {
        let net = Contiguous::<IpNetwork>::parse("2001:db8::/32").unwrap();
        let msg = pb::IpPrefix::from(net);
        let json = serde_json::to_string(&msg).unwrap();
        assert_eq!(r#""2001:db8::/32""#, json);
        let got: pb::IpPrefix = serde_json::from_str(&json).unwrap();
        assert_eq!(msg, got);
    }

    /// The `"invalid"` literal a malformed message serializes to is not
    /// itself a parseable network, so deserializing it back is a
    /// deliberate error rather than a silently reconstructed message.
    #[test]
    fn serde_contiguous_ip_network_invalid_does_not_deserialize() {
        let malformed = pb::IpPrefix { addr: None, prefix_len: 24 };
        let json = serde_json::to_string(&malformed).unwrap();
        assert_eq!(r#""invalid""#, json);
        assert!(serde_json::from_str::<pb::IpPrefix>(&json).is_err());
    }

    /// Verifies that the first address octet lands in the most
    /// significant byte of the encoded value, in both directions.
    ///
    /// The fixture values mirror the Go tests so a byte-order defect
    /// fails identically in both languages.
    #[test]
    fn test_ipv4_address_conversion_network_byte_order() {
        let addr = pb::IPv4Address::from(Ipv4Addr::new(10, 1, 2, 3));
        assert_eq!(0x0a010203, addr.addr);
        assert_eq!(Ipv4Addr::new(10, 1, 2, 3), Ipv4Addr::from(&addr));
    }

    /// Verifies the fixed32 wire encoding against a golden byte fixture
    /// shared with the Go tests.
    #[test]
    fn test_ipv4_address_wire_bytes_golden() {
        use prost::Message;

        let addr = pb::IPv4Address { addr: 0x0a010203 };
        assert_eq!(vec![0x0d, 0x03, 0x02, 0x01, 0x0a], addr.encode_to_vec());
    }

    #[test]
    fn test_ipv4_address_display_boundary_values() {
        assert_eq!("0.0.0.0", pb::IPv4Address { addr: 0 }.to_string());
        assert_eq!("255.255.255.255", pb::IPv4Address { addr: u32::MAX }.to_string());
    }

    #[test]
    fn test_ipv4_address_serde_string_round_trip() {
        let addr = pb::IPv4Address::from(Ipv4Addr::new(10, 1, 2, 3));
        let json = serde_json::to_string(&addr).unwrap();
        assert_eq!(r#""10.1.2.3""#, json);
        let got: pb::IPv4Address = serde_json::from_str(&json).unwrap();
        assert_eq!(addr, got);
    }

    #[test]
    fn test_ipv4_address_from_str_rejects_ipv6() {
        assert!("2001:db8::1".parse::<pb::IPv4Address>().is_err());
    }

    #[test]
    fn test_ipv4_address_from_str_rejects_ipv4_mapped() {
        assert!("::ffff:10.1.2.3".parse::<pb::IPv4Address>().is_err());
    }

    /// Verifies that address bytes 0-7 land in the high half and bytes
    /// 8-15 in the low half, both big-endian, in both directions.
    ///
    /// The fixture values mirror the Go tests so a half-swap or
    /// endianness defect fails identically in both languages.
    #[test]
    fn test_ipv6_address_conversion_network_byte_order() {
        let source = Ipv6Addr::new(0x2a02, 0x6b8, 0, 1, 0, 0, 0, 0x100);
        let addr = pb::IPv6Address::from(source);
        assert_eq!(0x2a0206b800000001, addr.hi);
        assert_eq!(0x0000000000000100, addr.lo);
        assert_eq!(source, Ipv6Addr::from(&addr));
    }

    /// Verifies the two-fixed64 wire encoding against a golden byte
    /// fixture shared with the Go tests.
    #[test]
    fn test_ipv6_address_wire_bytes_golden() {
        use prost::Message;

        let addr = pb::IPv6Address {
            hi: 0x2a0206b800000001,
            lo: 0x0000000000000100,
        };
        assert_eq!(
            vec![
                0x09, 0x01, 0x00, 0x00, 0x00, 0xb8, 0x06, 0x02, 0x2a, 0x11, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00,
                0x00,
            ],
            addr.encode_to_vec()
        );
    }

    #[test]
    fn test_ipv6_address_display_boundary_values() {
        assert_eq!("::", pb::IPv6Address { hi: 0, lo: 0 }.to_string());
        assert_eq!(
            "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
            pb::IPv6Address { hi: u64::MAX, lo: u64::MAX }.to_string()
        );
    }

    /// Verifies that the IPv4-mapped form stays in its mapped rendering
    /// instead of collapsing to the bare IPv4 string.
    #[test]
    fn test_ipv6_address_display_keeps_ipv4_mapped_form() {
        let addr = pb::IPv6Address { hi: 0, lo: 0x0000ffff0a010203 };
        assert_eq!("::ffff:10.1.2.3", addr.to_string());
    }

    #[test]
    fn test_ipv6_address_serde_string_round_trip() {
        let addr = pb::IPv6Address::from(Ipv6Addr::new(0x2a02, 0x6b8, 0, 1, 0, 0, 0, 0x100));
        let json = serde_json::to_string(&addr).unwrap();
        assert_eq!(r#""2a02:6b8:0:1::100""#, json);
        let got: pb::IPv6Address = serde_json::from_str(&json).unwrap();
        assert_eq!(addr, got);
    }

    #[test]
    fn test_ipv6_address_from_str_rejects_ipv4() {
        assert!("10.1.2.3".parse::<pb::IPv6Address>().is_err());
    }

    #[test]
    fn test_ipv6_address_from_str_rejects_zoned() {
        assert!("fe80::1%eth0".parse::<pb::IPv6Address>().is_err());
    }

    /// Verifies typed construction from a CIDR block and the decode back,
    /// with fixtures mirrored in the Go tests.
    #[test]
    fn test_contiguous_ipv4_network_conversion_round_trip() {
        let net = Contiguous::<Ipv4Network>::parse("10.1.2.0/24").unwrap();
        let msg = pb::IPv4Prefix::from(net);
        assert_eq!(Some(pb::IPv4Address { addr: 0x0a010200 }), msg.addr);
        assert_eq!(24, msg.prefix_len);
        assert_eq!(net, Contiguous::<Ipv4Network>::try_from(&msg).unwrap());
    }

    /// Verifies the nested wire encoding against a golden byte fixture
    /// shared with the Go tests.
    #[test]
    fn test_contiguous_ipv4_network_wire_bytes_golden() {
        use prost::Message;

        let msg = pb::IPv4Prefix {
            addr: Some(pb::IPv4Address { addr: 0x0a010203 }),
            prefix_len: 32,
        };
        assert_eq!(
            vec![0x0a, 0x05, 0x0d, 0x03, 0x02, 0x01, 0x0a, 0x10, 0x20],
            msg.encode_to_vec()
        );
    }

    #[test]
    fn test_contiguous_ipv4_network_try_from_masks_host_bits() {
        let msg = pb::IPv4Prefix {
            addr: Some(pb::IPv4Address { addr: 0x0a010203 }),
            prefix_len: 24,
        };
        let expected = Contiguous::<Ipv4Network>::parse("10.1.2.0/24").unwrap();
        assert_eq!(expected, Contiguous::<Ipv4Network>::try_from(&msg).unwrap());
    }

    #[test]
    fn test_contiguous_ipv4_network_try_from_rejects_absent_addr() {
        let malformed = pb::IPv4Prefix { addr: None, prefix_len: 24 };
        assert!(Contiguous::<Ipv4Network>::try_from(&malformed).is_err());
    }

    #[test]
    fn test_contiguous_ipv4_network_try_from_rejects_prefix_overflow() {
        let malformed = pb::IPv4Prefix {
            addr: Some(pb::IPv4Address { addr: 0x0a000000 }),
            prefix_len: 33,
        };
        assert!(Contiguous::<Ipv4Network>::try_from(&malformed).is_err());
    }

    #[test]
    fn test_contiguous_ipv4_network_serde_string_round_trip() {
        let msg = pb::IPv4Prefix::from(Contiguous::<Ipv4Network>::parse("10.0.0.0/8").unwrap());
        let json = serde_json::to_string(&msg).unwrap();
        assert_eq!(r#""10.0.0.0/8""#, json);
        let got: pb::IPv4Prefix = serde_json::from_str(&json).unwrap();
        assert_eq!(msg, got);
    }

    /// The `"invalid"` literal an absent address serializes to is not
    /// itself a parseable network, so deserializing it back is a
    /// deliberate error rather than a silently reconstructed message.
    #[test]
    fn test_contiguous_ipv4_network_serde_invalid_does_not_deserialize() {
        let malformed = pb::IPv4Prefix { addr: None, prefix_len: 24 };
        let json = serde_json::to_string(&malformed).unwrap();
        assert_eq!(r#""invalid""#, json);
        assert!(serde_json::from_str::<pb::IPv4Prefix>(&json).is_err());
    }

    #[test]
    fn test_contiguous_ipv4_network_from_str_masks_host_bits() {
        let got: pb::IPv4Prefix = "10.1.2.3/24".parse().unwrap();
        assert_eq!(Some(pb::IPv4Address { addr: 0x0a010200 }), got.addr);
        assert_eq!(24, got.prefix_len);
    }

    #[test]
    fn test_contiguous_ipv4_network_from_str_rejects_ipv6() {
        assert!("2a02:6b8::/32".parse::<pb::IPv4Prefix>().is_err());
    }

    /// The default route is not an empty message: the present-but-zero
    /// address encodes as two bytes, and decode relies on that presence
    /// to tell the default route from a malformed message.
    #[test]
    fn test_contiguous_ipv4_network_wire_bytes_zero_value_golden() {
        use prost::Message;

        let msg = pb::IPv4Prefix {
            addr: Some(pb::IPv4Address { addr: 0 }),
            prefix_len: 0,
        };
        assert_eq!(vec![0x0a, 0x00], msg.encode_to_vec());
    }

    /// Verifies typed construction from a CIDR block and the decode back,
    /// with fixtures mirrored in the Go tests.
    #[test]
    fn test_contiguous_ipv6_network_conversion_round_trip() {
        let net = Contiguous::<Ipv6Network>::parse("2a02:6b8::/32").unwrap();
        let msg = pb::IPv6Prefix::from(net);
        assert_eq!(Some(pb::IPv6Address { hi: 0x2a0206b800000000, lo: 0 }), msg.addr);
        assert_eq!(32, msg.prefix_len);
        assert_eq!(net, Contiguous::<Ipv6Network>::try_from(&msg).unwrap());
    }

    /// Verifies the nested wire encoding against a golden byte fixture
    /// shared with the Go tests.
    #[test]
    fn test_contiguous_ipv6_network_wire_bytes_golden() {
        use prost::Message;

        let msg = pb::IPv6Prefix {
            addr: Some(pb::IPv6Address {
                hi: 0x2a0206b800000001,
                lo: 0x0000000000000100,
            }),
            prefix_len: 128,
        };
        assert_eq!(
            vec![
                0x0a, 0x12, 0x09, 0x01, 0x00, 0x00, 0x00, 0xb8, 0x06, 0x02, 0x2a, 0x11, 0x00, 0x01, 0x00, 0x00, 0x00,
                0x00, 0x00, 0x00, 0x10, 0x80, 0x01,
            ],
            msg.encode_to_vec()
        );
    }

    /// The default route is not an empty message: the present-but-zero
    /// address encodes as two bytes, and decode relies on that presence
    /// to tell the default route from a malformed message.
    #[test]
    fn test_contiguous_ipv6_network_wire_bytes_zero_value_golden() {
        use prost::Message;

        let msg = pb::IPv6Prefix {
            addr: Some(pb::IPv6Address { hi: 0, lo: 0 }),
            prefix_len: 0,
        };
        assert_eq!(vec![0x0a, 0x00], msg.encode_to_vec());
    }

    #[test]
    fn test_contiguous_ipv6_network_try_from_masks_host_bits() {
        let msg = pb::IPv6Prefix {
            addr: Some(pb::IPv6Address {
                hi: 0x2a0206b800000001,
                lo: 0x0000000000000100,
            }),
            prefix_len: 64,
        };
        let expected = Contiguous::<Ipv6Network>::parse("2a02:6b8:0:1::/64").unwrap();
        assert_eq!(expected, Contiguous::<Ipv6Network>::try_from(&msg).unwrap());
    }

    #[test]
    fn test_contiguous_ipv6_network_try_from_rejects_absent_addr() {
        let malformed = pb::IPv6Prefix { addr: None, prefix_len: 64 };
        assert!(Contiguous::<Ipv6Network>::try_from(&malformed).is_err());
    }

    #[test]
    fn test_contiguous_ipv6_network_try_from_rejects_prefix_overflow() {
        let malformed = pb::IPv6Prefix {
            addr: Some(pb::IPv6Address { hi: 0x2a0206b800000000, lo: 0 }),
            prefix_len: 129,
        };
        assert!(Contiguous::<Ipv6Network>::try_from(&malformed).is_err());
    }

    #[test]
    fn test_contiguous_ipv6_network_serde_string_round_trip() {
        let msg = pb::IPv6Prefix::from(Contiguous::<Ipv6Network>::parse("2a02:6b8::/32").unwrap());
        let json = serde_json::to_string(&msg).unwrap();
        assert_eq!(r#""2a02:6b8::/32""#, json);
        let got: pb::IPv6Prefix = serde_json::from_str(&json).unwrap();
        assert_eq!(msg, got);
    }

    /// The `"invalid"` literal an absent address serializes to is not
    /// itself a parseable network, so deserializing it back is a
    /// deliberate error rather than a silently reconstructed message.
    #[test]
    fn test_contiguous_ipv6_network_serde_invalid_does_not_deserialize() {
        let malformed = pb::IPv6Prefix { addr: None, prefix_len: 64 };
        let json = serde_json::to_string(&malformed).unwrap();
        assert_eq!(r#""invalid""#, json);
        assert!(serde_json::from_str::<pb::IPv6Prefix>(&json).is_err());
    }

    #[test]
    fn test_contiguous_ipv6_network_from_str_masks_host_bits() {
        let got: pb::IPv6Prefix = "2a02:6b8:0:1::100/64".parse().unwrap();
        assert_eq!(Some(pb::IPv6Address { hi: 0x2a0206b800000001, lo: 0 }), got.addr);
        assert_eq!(64, got.prefix_len);
    }

    #[test]
    fn test_contiguous_ipv6_network_from_str_rejects_ipv4() {
        assert!("10.0.0.0/8".parse::<pb::IPv6Prefix>().is_err());
    }

    /// A half-zero address omits that half inside the nested message;
    /// the fixture pins the hi-only shape shared with the Go tests.
    #[test]
    fn test_contiguous_ipv6_network_wire_bytes_hi_only_golden() {
        use prost::Message;

        let msg = pb::IPv6Prefix {
            addr: Some(pb::IPv6Address { hi: 0x2a0206b800000000, lo: 0 }),
            prefix_len: 32,
        };
        assert_eq!(
            vec![
                0x0a, 0x09, 0x09, 0x00, 0x00, 0x00, 0x00, 0xb8, 0x06, 0x02, 0x2a, 0x10, 0x20
            ],
            msg.encode_to_vec()
        );
    }
}
