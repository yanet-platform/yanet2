use core::{
    error::Error,
    fmt::{self, Display, Formatter},
    net::{IpAddr, Ipv4Addr, Ipv6Addr},
    str::FromStr,
};

#[allow(clippy::all, non_snake_case)]
pub mod pb {
    tonic::include_proto!("commonpb");
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
            Ok(addr) => addr.fmt(f),
            Err(_) => f.write_str("invalid"),
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
}
