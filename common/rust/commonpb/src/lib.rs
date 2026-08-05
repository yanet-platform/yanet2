use core::{
    error::Error,
    fmt::{self, Display, Formatter},
    net::{IpAddr, Ipv4Addr, Ipv6Addr},
    str::FromStr,
};

use netip::{Contiguous, IpNetwork, MacAddr, ipv4_range_to_networks, ipv6_range_to_networks};

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

impl Display for pb::MacAddress {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match MacAddr::try_from(self) {
            Ok(mac) => mac.fmt(f),
            Err(..) => f.write_str("invalid"),
        }
    }
}

impl serde::Serialize for pb::MacAddress {
    /// Serializes as the string `Display` renders.
    ///
    /// A message with the upper 16 bits set renders as the literal
    /// `"invalid"`, since that is what `Display` already falls back to.
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: serde::Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> serde::Deserialize<'de> for pb::MacAddress {
    /// Parses `aa:bb:cc:dd:ee:ff` via `MacAddr`'s own `FromStr`.
    ///
    /// The literal `"invalid"` a set upper 16 bits serializes to is not
    /// itself a parseable MAC address, so it fails here with a
    /// deserialization error instead of reconstructing one -- the same
    /// deliberately lossy treatment `pb::IpAddress` gives its own
    /// malformed case.
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        let mac = s.parse::<MacAddr>().map_err(serde::de::Error::custom)?;
        Ok(Self::from(mac))
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

impl serde::Serialize for pb::IpAddress {
    /// Serializes as the plain address string `Display` renders.
    ///
    /// A malformed byte length renders as the literal `"invalid"`, since
    /// that is what `Display` already falls back to. An IPv4-mapped IPv6
    /// address renders as its unmapped 4-byte form too, so the round trip
    /// through this string is lossy for that one input shape as well.
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: serde::Serializer,
    {
        serializer.collect_str(self)
    }
}

impl<'de> serde::Deserialize<'de> for pb::IpAddress {
    /// Parses the string `Serialize` produces, via `FromStr`.
    ///
    /// The literal `"invalid"` a malformed byte length serializes to is
    /// not itself a parseable address, so it fails here with a
    /// deserialization error instead of reconstructing some address for
    /// it. A malformed `IpAddress` is therefore lossy across this string
    /// encoding, deliberately, rather than round-tripping.
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        s.parse::<Self>().map_err(serde::de::Error::custom)
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

#[cfg(test)]
mod test {
    use super::*;

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
}
