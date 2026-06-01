use std::fmt::{self, Display, Formatter};

use netip::{Contiguous, IpNetwork, Ipv4Network, Ipv6Network};
use serde::{Deserialize, Deserializer, Serialize, Serializer, de};

use crate::pb::{Device, IpNet};

impl Display for IpNet {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match (self.addr.len(), self.mask.len()) {
            (4, 4) => {
                let addr = u32::from_be_bytes(<[u8; 4]>::try_from(self.addr.as_slice()).expect("checked above"));
                let mask = u32::from_be_bytes(<[u8; 4]>::try_from(self.mask.as_slice()).expect("checked above"));
                Ipv4Network::from_bits(addr, mask).fmt(f)
            }
            (16, 16) => {
                let addr = u128::from_be_bytes(<[u8; 16]>::try_from(self.addr.as_slice()).expect("checked above"));
                let mask = u128::from_be_bytes(<[u8; 16]>::try_from(self.mask.as_slice()).expect("checked above"));
                Ipv6Network::from_bits(addr, mask).fmt(f)
            }
            (a, n) => write!(f, "<invalid IpNet: addr={a}B mask={n}B>"),
        }
    }
}

impl Serialize for IpNet {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.collect_str(self)
    }
}

impl<'de> Deserialize<'de> for IpNet {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        let s = String::deserialize(d)?;
        IpNetwork::parse(&s)
            .map(IpNet::from)
            .map_err(|e| de::Error::custom(format!("invalid network address `{s}`: {e}")))
    }
}

impl From<IpNetwork> for IpNet {
    fn from(net: IpNetwork) -> Self {
        match net {
            IpNetwork::V4(net) => Self {
                addr: net.addr().octets().to_vec(),
                mask: net.mask().octets().to_vec(),
            },
            IpNetwork::V6(net) => Self {
                addr: net.addr().octets().to_vec(),
                mask: net.mask().octets().to_vec(),
            },
        }
    }
}

impl From<Contiguous<IpNetwork>> for IpNet {
    fn from(net: Contiguous<IpNetwork>) -> Self {
        Self::from(*net)
    }
}

impl From<String> for Device {
    fn from(name: String) -> Self {
        Self { name }
    }
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn ipnet_v4_cidr_roundtrip() {
        let net: IpNet = serde_json::from_str("\"192.0.2.0/24\"").expect("IpNet parse must not fail");
        let serialized = serde_json::to_string(&net).expect("IpNet serialization must not fail");
        assert_eq!("\"192.0.2.0/24\"", serialized);
    }

    #[test]
    fn ipnet_v4_mask_parse() {
        let net: IpNet = serde_json::from_str("\"192.0.2.0/255.255.255.0\"").expect("IpNet mask parse must not fail");
        let serialized = serde_json::to_string(&net).expect("IpNet serialization must not fail");
        assert_eq!("\"192.0.2.0/24\"", serialized);
    }

    #[test]
    fn ipnet_v6_cidr_roundtrip() {
        let net: IpNet = serde_json::from_str("\"2001:db8::/32\"").expect("IpNet parse must not fail");
        let serialized = serde_json::to_string(&net).expect("IpNet serialization must not fail");
        assert_eq!("\"2001:db8::/32\"", serialized);
    }

    #[test]
    fn device_object_roundtrip() {
        let device: Device = serde_json::from_str(r#"{"name":"eth0"}"#).expect("Device parse must not fail");
        assert_eq!("eth0", device.name);
        let serialized = serde_json::to_string(&device).expect("Device serialization must not fail");
        assert_eq!(r#"{"name":"eth0"}"#, serialized);
    }

    #[test]
    fn device_empty_name_object_roundtrip() {
        let device: Device = serde_json::from_str(r#"{"name":""}"#).expect("Device parse must not fail");
        assert_eq!("", device.name);
        let serialized = serde_json::to_string(&device).expect("Device serialization must not fail");
        assert_eq!(r#"{"name":""}"#, serialized);
    }
}
