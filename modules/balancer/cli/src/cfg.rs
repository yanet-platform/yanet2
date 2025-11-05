use std::{error::Error, string::FromUtf8Error};

use serde::{Deserialize, Serialize};

use crate::rpc::balancerpb;

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

#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
struct VsFlags {
    gre: bool,
    ops: bool,
    fix_mss: bool,
    pure_l3: bool,
}

#[derive(Debug, Serialize, Deserialize)]
struct VirtualService {
    ip: String,
    proto: String,
    port: u16,
    flags: VsFlags,
    allowed_srcs: Vec<String>,
    reals: Vec<Real>,
}

impl From<VirtualService> for balancerpb::VirtualService {
    fn from(vs: VirtualService) -> Self {
        Self {
            addr: vs.ip.into_bytes(),
            port: vs.port as u32,
            proto: vs.proto,
            allowed_srcs: vs.allowed_srcs,
            reals: vs.reals.into_iter().map(Into::into).collect(),
            gre: vs.flags.gre,
            fix_mss: vs.flags.fix_mss,
            ops: vs.flags.ops,
            pure_l3: vs.flags.pure_l3,
        }
    }
}

impl TryFrom<balancerpb::VirtualService> for VirtualService {
    type Error = FromUtf8Error;
    fn try_from(vs: balancerpb::VirtualService) -> Result<Self, Self::Error> {
        Ok(Self {
            ip: String::from_utf8(vs.addr)?,
            proto: vs.proto,
            port: vs.port as u16,
            flags: VsFlags {
                gre: vs.gre,
                ops: vs.ops,
                fix_mss: vs.fix_mss,
                pure_l3: vs.pure_l3,
            },
            allowed_srcs: vs.allowed_srcs,
            reals: vs
                .reals
                .into_iter()
                .map(TryFrom::try_from)
                .collect::<Result<Vec<_>, _>>()?,
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
    timeouts: SessionsTimeouts,
    vs: Vec<VirtualService>,
}

impl From<BalancerConfig> for balancerpb::BalancerInstanceConfig {
    fn from(cfg: BalancerConfig) -> Self {
        Self {
            sessions_timeouts: Some(cfg.timeouts.into()),
            virtual_services: cfg.vs.into_iter().map(Into::into).collect(),
        }
    }
}

impl TryFrom<balancerpb::BalancerInstanceConfig> for BalancerConfig {
    type Error = Box<dyn Error>;
    fn try_from(cfg: balancerpb::BalancerInstanceConfig) -> Result<Self, Self::Error> {
        Ok(Self {
            timeouts: cfg
                .sessions_timeouts
                .ok_or("sessions timeouts not specified")?
                .into(),
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
        let config = r#"timeouts:
  tcp_syn_ack: 10
  tcp_syn: 10
  tcp_fin: 10
  tcp: 20
  udp: 30
  default: 60
vs:
  - ip: "195.13.22.16"
    proto: "TCP"
    port: 5005
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
        assert_eq!(
            cfg.timeouts,
            SessionsTimeouts {
                tcp_syn_ack: 10,
                tcp_syn: 10,
                tcp_fin: 10,
                tcp: 20,
                udp: 30,
                default: 60
            }
        );
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
