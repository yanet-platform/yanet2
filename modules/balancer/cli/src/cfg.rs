use std::{
    error::Error,
    net::{IpAddr, Ipv4Addr, Ipv6Addr},
    string::FromUtf8Error,
};

use serde::{Deserialize, Serialize};

use crate::rpc::balancerpb;

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
struct Net {
    addr: String,
    size: u32,
}

impl From<Net> for balancerpb::Subnet {
    fn from(value: Net) -> Self {
        let net: IpAddr = value.addr.parse().unwrap();
        Self {
            addr: match net {
                IpAddr::V4(ipv4) => ipv4.octets().into(),
                IpAddr::V6(ipv6) => ipv6.octets().into(),
            },
            size: value.size,
        }
    }
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
struct Real {
    weight: u16,
    src: String,
    src_mask: String,
    dst: String,
    enabled: bool,
}

impl From<Real> for balancerpb::Real {
    fn from(real: Real) -> Self {
        Self {
            weight: real.weight as u32,
            dst_addr: real.dst.into_bytes(),
            src_addr: real.src.into_bytes(),
            src_mask: real.src_mask.into_bytes(),
            enabled: real.enabled,
        }
    }
}

impl TryFrom<balancerpb::Real> for Real {
    type Error = FromUtf8Error;

    fn try_from(real: balancerpb::Real) -> Result<Self, Self::Error> {
        Ok(Self {
            weight: real.weight as u16,
            dst: String::from_utf8(real.dst_addr)?,
            src: String::from_utf8(real.src_addr)?,
            src_mask: String::from_utf8(real.src_mask)?,
            enabled: real.enabled,
        })
    }
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
struct VsFlags {
    gre: bool,
    ops: bool,
    fix_mss: bool,
    pure_l3: bool,
}

impl From<balancerpb::VsFlags> for VsFlags {
    fn from(flags: balancerpb::VsFlags) -> Self {
        Self {
            gre: flags.gre,
            ops: flags.ops,
            fix_mss: flags.fix_mss,
            pure_l3: flags.pure_l3,
        }
    }
}

impl From<VsFlags> for balancerpb::VsFlags {
    fn from(flags: VsFlags) -> Self {
        Self {
            gre: flags.gre,
            ops: flags.ops,
            fix_mss: flags.fix_mss,
            pure_l3: flags.pure_l3,
        }
    }
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
struct VirtualService {
    ip: String,
    proto: String,
    port: u16,
    scheduler: String,
    flags: VsFlags,
    allowed_srcs: Vec<String>,
    reals: Vec<Real>,
}

impl TryFrom<VirtualService> for balancerpb::VirtualService {
    type Error = String;
    fn try_from(vs: VirtualService) -> Result<Self, Self::Error> {
        let proto = vs.proto.to_lowercase();
        let proto = if proto == "tcp" {
            balancerpb::TransportProto::Tcp
        } else if proto == "udp" {
            balancerpb::TransportProto::Udp
        } else {
            return Err("invalid transport protocol".to_string());
        };

        let scheduler = vs.scheduler.to_lowercase();
        let scheduler = if scheduler == "wrr" {
            balancerpb::VsScheduler::Wrr
        } else if scheduler == "prr" {
            balancerpb::VsScheduler::Prr
        } else {
            return Err("invalid scheduler".to_string());
        };

        let allowed_srcs: Result<Vec<balancerpb::Subnet>, String> = vs
            .allowed_srcs
            .into_iter()
            .enumerate()
            .map(|(i, s)| {
                let mut split = s.split('/');
                let addr = split.next().unwrap();
                let size = split.next().unwrap();
                if split.next().is_some() {
                    return Err(format!("invalid allowed src specified: {}", i + 1));
                }
                let Ok(size) = size.parse() else {
                    return Err(format!("invalid allowed src subnet size specified: {}", i + 1));
                };
                let Ok(net) = addr.parse::<IpAddr>() else {
                    return Err(format!("failed to parse address for subnet: {}", i + 1));
                };
                let addr = match net {
                    IpAddr::V4(ipv4) => ipv4.octets().into(),
                    IpAddr::V6(ipv6) => ipv6.octets().into(),
                };
                Ok(balancerpb::Subnet { addr, size })
            })
            .collect();
        let allowed_srcs = allowed_srcs?;

        Ok(Self {
            addr: vs.ip.into_bytes(),
            port: vs.port as u32,
            proto: proto as i32,
            allowed_srcs,
            reals: vs.reals.into_iter().map(Into::into).collect(),
            flags: Some(vs.flags.into()),
            scheduler: scheduler as i32,
        })
    }
}

impl TryFrom<balancerpb::VirtualService> for VirtualService {
    type Error = FromUtf8Error;
    fn try_from(vs: balancerpb::VirtualService) -> Result<Self, Self::Error> {
        // get proto
        let proto = match vs.proto() {
            balancerpb::TransportProto::Tcp => "tcp",
            balancerpb::TransportProto::Udp => "udp",
        }
        .to_string();

        // get scheduler
        let scheduler = match vs.scheduler() {
            balancerpb::VsScheduler::Wrr => "wrr",
            balancerpb::VsScheduler::Prr => "prr",
            balancerpb::VsScheduler::Wlc => "wlc",
        }
        .to_string();

        // get allowed packet sources
        let allowed_srcs = vs
            .allowed_srcs
            .into_iter()
            .map(|s| {
                let net: IpAddr = match s.addr.len() {
                    4 => {
                        let bytes: [u8; 4] = s.addr.try_into().unwrap();
                        IpAddr::V4(Ipv4Addr::from(bytes))
                    }
                    16 => {
                        let bytes: [u8; 16] = s.addr.try_into().unwrap();
                        IpAddr::V6(Ipv6Addr::from(bytes))
                    }
                    _ => panic!("got bad subnet address, {:?}", s.addr),
                };
                net.to_string()
            })
            .collect();

        // get reals

        let reals = vs
            .reals
            .into_iter()
            .map(TryFrom::try_from)
            .collect::<Result<Vec<_>, _>>()?;

        Ok(Self {
            ip: String::from_utf8(vs.addr)?,
            proto,
            port: vs.port as u16,
            flags: vs.flags.unwrap().into(),
            allowed_srcs,
            reals,
            scheduler,
        })
    }
}

#[derive(Debug, Deserialize, Serialize, PartialEq, Eq)]
struct SessionsTimeouts {
    tcp_syn_ack: u32,
    tcp_syn: u32,
    tcp_fin: u32,
    tcp: u32,
    udp: u32,
    default: u32,
}

impl From<SessionsTimeouts> for balancerpb::SessionsTimeouts {
    fn from(timeouts: SessionsTimeouts) -> Self {
        Self {
            tcp_syn_ack: timeouts.tcp_syn_ack,
            tcp_syn: timeouts.tcp_syn,
            tcp_fin: timeouts.tcp_fin,
            tcp: timeouts.tcp,
            udp: timeouts.udp,
            default: timeouts.default,
        }
    }
}

impl From<balancerpb::SessionsTimeouts> for SessionsTimeouts {
    fn from(timeouts: balancerpb::SessionsTimeouts) -> Self {
        Self {
            tcp_syn_ack: timeouts.tcp_syn_ack,
            tcp_syn: timeouts.tcp_syn,
            tcp_fin: timeouts.tcp_fin,
            tcp: timeouts.tcp,
            udp: timeouts.udp,
            default: timeouts.default,
        }
    }
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Serialize, Deserialize)]
pub struct BalancerConfig {
    vs: Vec<VirtualService>,
}

impl TryFrom<BalancerConfig> for balancerpb::BalancerInstanceConfig {
    type Error = String;
    fn try_from(cfg: BalancerConfig) -> Result<Self, Self::Error> {
        let vs: Result<Vec<balancerpb::VirtualService>, Self::Error> =
            cfg.vs.into_iter().map(TryInto::try_into).collect();
        let vs = vs?;
        Ok(Self { virtual_services: vs })
    }
}

impl TryFrom<balancerpb::BalancerInstanceConfig> for BalancerConfig {
    type Error = Box<dyn Error>;
    fn try_from(cfg: balancerpb::BalancerInstanceConfig) -> Result<Self, Self::Error> {
        Ok(Self {
            vs: cfg
                .virtual_services
                .into_iter()
                .map(TryFrom::try_from)
                .collect::<Result<Vec<_>, _>>()?,
        })
    }
}

impl BalancerConfig {
    pub fn from_file(path: &str) -> Result<Self, Box<dyn Error>> {
        let file = std::fs::File::open(path)?;
        let config = serde_yaml::from_reader(file)?;
        Ok(config)
    }
}

////////////////////////////////////////////////////////////////////////////////

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn basic() {
        let config = r#"
vs:
  - ip: "195.13.22.16"
    proto: "TCP"
    port: 5005
    scheduler: "prr"
    flags:
      gre: false
      ops: false
      fix_mss: false
      pure_l3: false
    allowed_srcs:
      - "4.4.4.4/8"
    reals:
      - weight: 1
        dst: "1.1.1.1"
        src: "3.3.4.0"
        src_mask: "0.255.0.255"
        enabled: true
"#;

        let cfg: BalancerConfig = serde_yaml::from_str(config).unwrap();
        assert_eq!(cfg.vs.len(), 1);

        let vs = &cfg.vs[0];
        assert_eq!(
            vs.flags,
            VsFlags {
                gre: false,
                ops: false,
                fix_mss: false,
                pure_l3: false
            }
        );
        assert_eq!(vs.scheduler, "prr");
        assert_eq!(vs.port, 5005);
        assert_eq!(vs.proto, "TCP");
        assert_eq!(vs.allowed_srcs.len(), 1);
        assert_eq!(vs.allowed_srcs[0], "4.4.4.4/8");
        assert_eq!(vs.reals.len(), 1);

        let real = &vs.reals[0];
        assert_eq!(real.dst, "1.1.1.1");
        assert_eq!(real.src, "3.3.4.0");
        assert_eq!(real.src_mask, "0.255.0.255");
        assert_eq!(real.enabled, true);
    }
}
