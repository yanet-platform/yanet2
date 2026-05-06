use std::{
    error::Error,
    fmt::Display,
    fs::File,
    net::{IpAddr, Ipv4Addr, Ipv6Addr},
    str::FromStr,
};

use filterpb::pb::{IpNet, PortRange};
use netip::IpNetwork;
use serde::{Deserialize, Deserializer};
use yanet_cli_balancer2::balancerpb;

use crate::ip_to_bytes;

fn deserialize_from_str<'de, T, D>(deserializer: D) -> Result<T, D::Error>
where
    T: FromStr,
    T::Err: Display,
    D: Deserializer<'de>,
{
    let s = String::deserialize(deserializer)?;
    T::from_str(&s).map_err(serde::de::Error::custom)
}

#[derive(Debug, Clone, Deserialize)]
pub struct BalancerConfig {
    #[serde(default)]
    pub vs: Vec<VirtualService>,
    #[serde(default)]
    pub source_address_v4: Option<Ipv4Addr>,
    #[serde(default)]
    pub source_address_v6: Option<Ipv6Addr>,
    #[serde(default)]
    pub decap_addresses: Vec<IpAddr>,
    #[serde(default)]
    pub sessions_timeouts: Option<SessionsTimeouts>,
    #[serde(default)]
    pub wlc: Option<WlcConfig>,
}

impl BalancerConfig {
    pub fn from_yaml_file(path: &str) -> Result<Self, Box<dyn Error>> {
        Ok(serde_yaml::from_reader(File::open(path)?)?)
    }
}

#[derive(Debug, Clone, Deserialize)]
pub struct VirtualService {
    pub addr: IpAddr,
    pub port: u16,
    pub proto: Proto,
    pub scheduler: Scheduler,
    #[serde(default)]
    pub flags: VsFlags,
    #[serde(default)]
    pub allowed_sources: Vec<AllowedSrcEntry>,
    pub reals: Vec<Real>,
    #[serde(default)]
    pub peers: Vec<IpAddr>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Real {
    pub ip: IpAddr,
    #[serde(default)]
    pub port: u16,
    pub weight: u32,
    #[serde(deserialize_with = "deserialize_from_str")]
    pub src: IpNetwork,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Scheduler {
    Sh,
    Wrr,
    Wlc,
    Op,
}

// Mirrors main::Proto for YAML config; the two cannot share a type because of
// orphan-rule + derive constraints.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Proto {
    Tcp,
    Udp,
}

#[derive(Debug, Clone, Default, Deserialize)]
pub struct VsFlags {
    #[serde(default)]
    pub gre: bool,
    #[serde(default)]
    pub fix_mss: bool,
    #[serde(default)]
    pub pure_l3: bool,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SessionsTimeouts {
    pub tcp_syn_ack: u32,
    pub tcp_syn: u32,
    pub tcp_fin: u32,
    pub tcp: u32,
    pub udp: u32,
}

#[derive(Debug, Clone, Deserialize)]
pub struct WlcConfig {
    pub power: u32,
    pub max_weight: u32,
    #[serde(default)]
    pub refresh_period_ms: Option<u64>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(untagged)]
pub enum AllowedSrcEntry {
    Simple(#[serde(deserialize_with = "deserialize_from_str")] IpNetwork),
    Structured {
        #[serde(deserialize_with = "deserialize_from_str")]
        network: IpNetwork,
        #[serde(default)]
        ports: Vec<Range>,
        #[serde(default)]
        tag: Option<String>,
    },
}

#[derive(Debug, Clone, Deserialize)]
pub struct Range {
    pub from: u16,
    pub to: u16,
}

fn port_range(r: &Range) -> Result<PortRange, Box<dyn Error>> {
    if r.from > r.to {
        return Err(format!("port 'from' value {} is greater than 'to' value {}", r.from, r.to).into());
    }
    Ok(PortRange {
        from: u32::from(r.from),
        to: u32::from(r.to),
    })
}

pub struct ConfigParts {
    pub vs: Option<balancerpb::VsConfigList>,
    pub timeouts: Option<balancerpb::SessionsTimeouts>,
    pub addr: Option<balancerpb::AddrConfig>,
    pub wlc: Option<balancerpb::WlcConfig>,
}

impl TryFrom<BalancerConfig> for ConfigParts {
    type Error = Box<dyn Error>;

    fn try_from(config: BalancerConfig) -> Result<Self, Self::Error> {
        let vs = if config.vs.is_empty() {
            None
        } else {
            let entries: Vec<_> = config.vs.into_iter().map(TryInto::try_into).collect::<Result<_, _>>()?;
            Some(balancerpb::VsConfigList { vs: entries })
        };

        let addr = build_addr_config(
            config.source_address_v4,
            config.source_address_v6,
            &config.decap_addresses,
        );

        Ok(Self {
            vs,
            timeouts: config.sessions_timeouts.map(Into::into),
            addr,
            wlc: config.wlc.map(Into::into),
        })
    }
}

fn build_addr_config(v4: Option<Ipv4Addr>, v6: Option<Ipv6Addr>, decaps: &[IpAddr]) -> Option<balancerpb::AddrConfig> {
    if v4.is_none() && v6.is_none() && decaps.is_empty() {
        return None;
    }
    Some(balancerpb::AddrConfig {
        source_ip4: v4.map(|a| ip_to_bytes(IpAddr::V4(a))).unwrap_or_default(),
        source_ip6: v6.map(|a| ip_to_bytes(IpAddr::V6(a))).unwrap_or_default(),
        decaps: decaps.iter().copied().map(ip_to_bytes).collect(),
    })
}

impl TryFrom<VirtualService> for balancerpb::VsConfig {
    type Error = Box<dyn Error>;

    fn try_from(vs: VirtualService) -> Result<Self, Self::Error> {
        let proto = match vs.proto {
            Proto::Tcp => balancerpb::TransportProto::Tcp,
            Proto::Udp => balancerpb::TransportProto::Udp,
        };
        let scheduler = match vs.scheduler {
            Scheduler::Sh => balancerpb::VsScheduler::Sh,
            Scheduler::Wrr => balancerpb::VsScheduler::Wrr,
            Scheduler::Wlc => balancerpb::VsScheduler::Wlc,
            Scheduler::Op => balancerpb::VsScheduler::Op,
        };

        let allowed_sources = vs
            .allowed_sources
            .into_iter()
            .map(allowed_source)
            .collect::<Result<_, _>>()?;
        let reals = vs.reals.into_iter().map(Into::into).collect();

        Ok(Self {
            id: Some(balancerpb::VsIdentifier {
                addr: ip_to_bytes(vs.addr),
                port: u32::from(vs.port),
                proto: proto as i32,
            }),
            scheduler: scheduler as i32,
            allowed_sources,
            reals,
            flags: Some(vs.flags.into()),
            peers: vs.peers.into_iter().map(ip_to_bytes).collect(),
        })
    }
}

fn allowed_source(entry: AllowedSrcEntry) -> Result<balancerpb::AllowedSources, Box<dyn Error>> {
    match entry {
        AllowedSrcEntry::Simple(network) => Ok(balancerpb::AllowedSources {
            nets: vec![IpNet::from(network)],
            ports: vec![],
            tag: None,
        }),
        AllowedSrcEntry::Structured { network, ports, tag } => Ok(balancerpb::AllowedSources {
            nets: vec![IpNet::from(network)],
            ports: ports.iter().map(port_range).collect::<Result<_, _>>()?,
            tag,
        }),
    }
}

impl From<VsFlags> for balancerpb::VsFlags {
    fn from(flags: VsFlags) -> Self {
        Self {
            gre: flags.gre,
            fix_mss: flags.fix_mss,
            pure_l3: flags.pure_l3,
        }
    }
}

impl From<Real> for balancerpb::RealConfig {
    fn from(real: Real) -> Self {
        Self {
            id: Some(balancerpb::RelativeRealIdentifier {
                ip: ip_to_bytes(real.ip),
                port: u32::from(real.port),
            }),
            weight: real.weight,
            src: Some(IpNet::from(real.src)),
        }
    }
}

impl From<SessionsTimeouts> for balancerpb::SessionsTimeouts {
    fn from(timeouts: SessionsTimeouts) -> Self {
        Self {
            tcp_syn_ack: timeouts.tcp_syn_ack,
            tcp_syn: timeouts.tcp_syn,
            tcp_fin: timeouts.tcp_fin,
            tcp: timeouts.tcp,
            udp: timeouts.udp,
        }
    }
}

impl From<WlcConfig> for balancerpb::WlcConfig {
    fn from(config: WlcConfig) -> Self {
        Self {
            power: config.power,
            max_weight: config.max_weight,
            refresh_period: config.refresh_period_ms.map(|ms| prost_types::Duration {
                seconds: (ms / 1000) as i64,
                nanos: ((ms % 1000) * 1_000_000) as i32,
            }),
        }
    }
}
