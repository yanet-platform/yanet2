use std::net::{IpAddr, Ipv4Addr, Ipv6Addr};

use filterpb::pb::{IpNet, PortRange};
use serde::{Deserialize, Serialize};
use yanet_cli_balancer2::balancerpb;

use crate::ip_to_bytes;

// ─── YAML Config Types ───────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BalancerConfig {
    #[serde(default)]
    pub vs: Vec<VirtualService>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source_address_v4: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source_address_v6: Option<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub decap_addresses: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sessions_timeouts: Option<SessionsTimeouts>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub wlc: Option<WlcConfig>,
}

impl BalancerConfig {
    pub fn from_yaml_file(path: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let file = std::fs::File::open(path)?;
        let config = serde_yaml::from_reader(file)?;
        Ok(config)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct VirtualService {
    pub addr: String,
    pub port: u32,
    pub proto: Proto,
    pub scheduler: Scheduler,
    #[serde(default)]
    pub flags: VsFlags,
    #[serde(default)]
    pub allowed_sources: Vec<AllowedSrcEntry>,
    pub reals: Vec<Real>,
    #[serde(default)]
    pub peers: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Real {
    pub ip: String,
    #[serde(default)]
    pub port: u32,
    pub weight: u32,
    pub src_addr: String,
    pub src_mask: String,
}

#[derive(Debug, Clone, Serialize)]
pub enum Scheduler {
    Sh,
    Wrr,
    Wlc,
    Op,
}

impl<'de> Deserialize<'de> for Scheduler {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        match s.to_lowercase().as_str() {
            "sh" => Ok(Scheduler::Sh),
            "wrr" => Ok(Scheduler::Wrr),
            "wlc" => Ok(Scheduler::Wlc),
            "op" => Ok(Scheduler::Op),
            _ => Err(serde::de::Error::custom(format!(
                "invalid scheduler: '{}'. Expected: sh, wrr, wlc or op",
                s
            ))),
        }
    }
}

#[derive(Debug, Clone, Serialize)]
pub enum Proto {
    Tcp,
    Udp,
}

impl<'de> Deserialize<'de> for Proto {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        match s.to_uppercase().as_str() {
            "TCP" => Ok(Proto::Tcp),
            "UDP" => Ok(Proto::Udp),
            _ => Err(serde::de::Error::custom(format!(
                "invalid protocol: '{}'. Expected: TCP, UDP",
                s
            ))),
        }
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct VsFlags {
    #[serde(default)]
    pub gre: bool,
    #[serde(default)]
    pub fix_mss: bool,
    #[serde(default)]
    pub pure_l3: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionsTimeouts {
    pub tcp_syn_ack: u32,
    pub tcp_syn: u32,
    pub tcp_fin: u32,
    pub tcp: u32,
    pub udp: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WlcConfig {
    pub power: u32,
    pub max_weight: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub refresh_period_ms: Option<u64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum AllowedSrcEntry {
    Simple(String),
    Structured {
        network: String,
        #[serde(skip_serializing_if = "Option::is_none")]
        ports: Option<String>,
        #[serde(skip_serializing_if = "Option::is_none")]
        tag: Option<String>,
    },
}

// ─── Network Parsing ─────────────────────────────────────────────────────────

fn parse_network(network: &str) -> Result<(IpAddr, Vec<u8>), String> {
    let parts: Vec<&str> = network.split('/').collect();
    if parts.len() != 2 {
        return Err(format!("invalid network format: '{}'", network));
    }

    let addr: IpAddr = parts[0]
        .parse()
        .map_err(|e| format!("invalid IP in '{}': {}", network, e))?;

    if let Ok(prefix_len) = parts[1].parse::<u32>() {
        let max = if addr.is_ipv4() { 32 } else { 128 };
        if prefix_len > max {
            return Err(format!("prefix length {} exceeds max {}", prefix_len, max));
        }
        Ok((addr, prefix_to_mask(addr.is_ipv4(), prefix_len)))
    } else {
        let mask: IpAddr = parts[1]
            .parse()
            .map_err(|e| format!("invalid netmask in '{}': {}", network, e))?;
        match (addr, mask) {
            (IpAddr::V4(_), IpAddr::V4(m)) => Ok((addr, m.octets().to_vec())),
            (IpAddr::V6(_), IpAddr::V6(m)) => Ok((addr, m.octets().to_vec())),
            _ => Err("IP version mismatch between address and mask".to_string()),
        }
    }
}

fn prefix_to_mask(is_ipv4: bool, prefix_len: u32) -> Vec<u8> {
    if is_ipv4 {
        let mask: u32 = if prefix_len == 0 { 0 } else { !0u32 << (32 - prefix_len) };
        mask.to_be_bytes().to_vec()
    } else {
        let mut mask = [0u8; 16];
        let full_bytes = (prefix_len / 8) as usize;
        let remaining_bits = prefix_len % 8;
        for byte in mask.iter_mut().take(full_bytes) {
            *byte = 0xFF;
        }
        if remaining_bits > 0 && full_bytes < 16 {
            mask[full_bytes] = !0u8 << (8 - remaining_bits);
        }
        mask.to_vec()
    }
}

fn parse_ports(ports_str: &str) -> Result<Vec<PortRange>, String> {
    let mut ranges = Vec::new();
    for part in ports_str.split(',') {
        let part = part.trim();
        if part.is_empty() {
            continue;
        }
        if part.contains('-') {
            let ps: Vec<&str> = part.split('-').collect();
            if ps.len() != 2 {
                return Err(format!("invalid port range: '{}'", part));
            }
            let from: u32 = ps[0].trim().parse().map_err(|e| format!("invalid port: {}", e))?;
            let to: u32 = ps[1].trim().parse().map_err(|e| format!("invalid port: {}", e))?;
            ranges.push(PortRange { from, to });
        } else {
            let port: u32 = part.parse().map_err(|e| format!("invalid port: {}", e))?;
            ranges.push(PortRange { from: port, to: port });
        }
    }
    Ok(ranges)
}

// ─── Conversion to Proto ─────────────────────────────────────────────────────

pub struct ConfigParts {
    pub vs: Option<balancerpb::VsConfigList>,
    pub timeouts: Option<balancerpb::SessionsTimeouts>,
    pub addr: Option<balancerpb::AddrConfig>,
    pub wlc: Option<balancerpb::WlcConfig>,
}

impl TryFrom<BalancerConfig> for ConfigParts {
    type Error = String;

    fn try_from(config: BalancerConfig) -> Result<Self, Self::Error> {
        let vs = if config.vs.is_empty() {
            None
        } else {
            let entries: Vec<_> = config
                .vs
                .into_iter()
                .map(TryInto::try_into)
                .collect::<Result<_, String>>()?;
            Some(balancerpb::VsConfigList { vs: entries })
        };

        let addr = build_addr_config(
            config.source_address_v4.as_deref(),
            config.source_address_v6.as_deref(),
            &config.decap_addresses,
        )?;

        Ok(Self {
            vs,
            timeouts: config.sessions_timeouts.map(Into::into),
            addr,
            wlc: config.wlc.map(Into::into),
        })
    }
}

fn build_addr_config(
    v4: Option<&str>,
    v6: Option<&str>,
    decaps: &[String],
) -> Result<Option<balancerpb::AddrConfig>, String> {
    if v4.is_none() && v6.is_none() && decaps.is_empty() {
        return Ok(None);
    }

    let source_ip4 = match v4 {
        Some(s) => {
            let addr: Ipv4Addr = s.parse().map_err(|e| format!("invalid source IPv4 '{}': {}", s, e))?;
            addr.octets().to_vec()
        }
        None => Vec::new(),
    };
    let source_ip6 = match v6 {
        Some(s) => {
            let addr: Ipv6Addr = s.parse().map_err(|e| format!("invalid source IPv6 '{}': {}", s, e))?;
            addr.octets().to_vec()
        }
        None => Vec::new(),
    };

    let decap_addrs: Result<Vec<_>, String> = decaps
        .iter()
        .map(|s| {
            let addr: IpAddr = s.parse().map_err(|e| format!("invalid decap IP '{}': {}", s, e))?;
            Ok(ip_to_bytes(addr))
        })
        .collect();

    Ok(Some(balancerpb::AddrConfig {
        source_ip4,
        source_ip6,
        decaps: decap_addrs?,
    }))
}

impl TryFrom<VirtualService> for balancerpb::VsConfig {
    type Error = String;

    fn try_from(vs: VirtualService) -> Result<Self, Self::Error> {
        let addr: IpAddr = vs
            .addr
            .parse()
            .map_err(|e| format!("invalid VS IP '{}': {}", vs.addr, e))?;
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

        let allowed_sources: Result<Vec<_>, String> = vs
            .allowed_sources
            .iter()
            .map(|entry| match entry {
                AllowedSrcEntry::Simple(network_str) => {
                    let (addr, mask) = parse_network(network_str)?;
                    Ok(balancerpb::AllowedSources {
                        nets: vec![IpNet { addr: ip_to_bytes(addr), mask }],
                        ports: vec![],
                        tag: None,
                    })
                }
                AllowedSrcEntry::Structured { network, ports, tag } => {
                    let (addr, mask) = parse_network(network)?;
                    let port_ranges = match ports {
                        Some(s) => parse_ports(s)?,
                        None => vec![],
                    };
                    Ok(balancerpb::AllowedSources {
                        nets: vec![IpNet { addr: ip_to_bytes(addr), mask }],
                        ports: port_ranges,
                        tag: tag.clone(),
                    })
                }
            })
            .collect();

        let peers: Result<Vec<_>, String> = vs
            .peers
            .iter()
            .map(|p| {
                let ip: IpAddr = p.parse().map_err(|e| format!("invalid peer IP '{}': {}", p, e))?;
                Ok(ip_to_bytes(ip))
            })
            .collect();

        let reals: Result<Vec<_>, String> = vs.reals.into_iter().map(TryInto::try_into).collect();

        Ok(Self {
            id: Some(balancerpb::VsIdentifier {
                addr: ip_to_bytes(addr),
                port: vs.port,
                proto: proto as i32,
            }),
            scheduler: scheduler as i32,
            allowed_sources: allowed_sources?,
            reals: reals?,
            flags: Some(vs.flags.into()),
            peers: peers?,
        })
    }
}

impl From<VsFlags> for balancerpb::VsFlags {
    fn from(f: VsFlags) -> Self {
        Self {
            gre: f.gre,
            fix_mss: f.fix_mss,
            pure_l3: f.pure_l3,
        }
    }
}

impl TryFrom<Real> for balancerpb::RealConfig {
    type Error = String;

    fn try_from(real: Real) -> Result<Self, Self::Error> {
        let ip: IpAddr = real
            .ip
            .parse()
            .map_err(|e| format!("invalid real IP '{}': {}", real.ip, e))?;
        let src_addr: IpAddr = real
            .src_addr
            .parse()
            .map_err(|e| format!("invalid src_addr '{}': {}", real.src_addr, e))?;
        let src_mask: IpAddr = real
            .src_mask
            .parse()
            .map_err(|e| format!("invalid src_mask '{}': {}", real.src_mask, e))?;

        Ok(Self {
            id: Some(balancerpb::RelativeRealIdentifier { ip: ip_to_bytes(ip), port: real.port }),
            weight: real.weight,
            src: Some(IpNet {
                addr: ip_to_bytes(src_addr),
                mask: ip_to_bytes(src_mask),
            }),
        })
    }
}

impl From<SessionsTimeouts> for balancerpb::SessionsTimeouts {
    fn from(t: SessionsTimeouts) -> Self {
        Self {
            tcp_syn_ack: t.tcp_syn_ack,
            tcp_syn: t.tcp_syn,
            tcp_fin: t.tcp_fin,
            tcp: t.tcp,
            udp: t.udp,
        }
    }
}

impl From<WlcConfig> for balancerpb::WlcConfig {
    fn from(c: WlcConfig) -> Self {
        Self {
            power: c.power,
            max_weight: c.max_weight,
            refresh_period: c.refresh_period_ms.map(|ms| prost_types::Duration {
                seconds: (ms / 1000) as i64,
                nanos: ((ms % 1000) * 1_000_000) as i32,
            }),
        }
    }
}
