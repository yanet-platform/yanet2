use std::fmt::{self, Display, Formatter};

use netip::{Contiguous, IpNetwork, Ipv4Network, Ipv6Network};
use serde::{Serialize, Serializer};

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
