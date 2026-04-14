use std::net::{IpAddr, Ipv4Addr, Ipv6Addr};

use serde::{Deserialize, Serialize};
use yanet_cli_balancer::{balancerpb, filterpb};

use crate::ip_to_bytes;

// ─── YAML Config Types ───────────────────────────────────────────────────────

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BalancerConfig {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub packet_handler: Option<PacketHandlerConfig>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state: Option<StateConfig>,
}

impl BalancerConfig {
    pub fn from_yaml_file(path: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let file = std::fs::File::open(path)?;
        let config = serde_yaml::from_reader(file)?;
        Ok(config)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PacketHandlerConfig {
    pub vs: Vec<VirtualService>,
    pub source_address_v4: String,
    pub source_address_v6: String,
    pub decap_addresses: Vec<String>,
    pub sessions_timeouts: SessionsTimeouts,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct VirtualService {
    pub addr: String,
    pub port: u32,
    pub proto: Proto,
    pub scheduler: Scheduler,
    pub flags: VsFlags,
    #[serde(default)]
    pub allowed_srcs: Vec<AllowedSrcEntry>,
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
    SourceHash,
    RoundRobin,
}

impl<'de> Deserialize<'de> for Scheduler {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        match s.to_uppercase().as_str() {
            "SOURCE_HASH" | "SH" => Ok(Scheduler::SourceHash),
            "ROUND_ROBIN" | "RR" => Ok(Scheduler::RoundRobin),
            _ => Err(serde::de::Error::custom(format!(
                "invalid scheduler: '{}'. Expected: SOURCE_HASH, SH, ROUND_ROBIN, RR",
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
    pub ops: bool,
    #[serde(default)]
    pub pure_l3: bool,
    #[serde(default)]
    pub wlc: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionsTimeouts {
    pub tcp_syn_ack: u32,
    pub tcp_syn: u32,
    pub tcp_fin: u32,
    pub tcp: u32,
    pub udp: u32,
    pub default: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StateConfig {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub session_table: Option<SessionTableConfig>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub wlc: Option<WlcConfig>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub refresh_period_ms: Option<u64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionTableConfig {
    pub capacity: u64,
    pub max_load_factor: f32,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WlcConfig {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub power: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_weight: Option<u32>,
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

fn parse_ports(ports_str: &str) -> Result<Vec<filterpb::PortRange>, String> {
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
            ranges.push(filterpb::PortRange { from, to });
        } else {
            let port: u32 = part.parse().map_err(|e| format!("invalid port: {}", e))?;
            ranges.push(filterpb::PortRange { from: port, to: port });
        }
    }
    Ok(ranges)
}

// ─── Conversion to Proto ─────────────────────────────────────────────────────

impl TryFrom<BalancerConfig> for balancerpb::BalancerConfig {
    type Error = String;

    fn try_from(config: BalancerConfig) -> Result<Self, Self::Error> {
        Ok(Self {
            packet_handler: config.packet_handler.map(TryInto::try_into).transpose()?,
            state: config.state.map(Into::into),
        })
    }
}

impl TryFrom<PacketHandlerConfig> for balancerpb::PacketHandlerConfig {
    type Error = String;

    fn try_from(config: PacketHandlerConfig) -> Result<Self, Self::Error> {
        let vs: Result<Vec<_>, String> = config.vs.into_iter().map(TryInto::try_into).collect();

        let source_v4: Ipv4Addr = config
            .source_address_v4
            .parse()
            .map_err(|e| format!("invalid source IPv4: {}", e))?;
        let source_v6: Ipv6Addr = config
            .source_address_v6
            .parse()
            .map_err(|e| format!("invalid source IPv6: {}", e))?;

        let decap: Result<Vec<_>, String> = config
            .decap_addresses
            .into_iter()
            .map(|s| {
                let addr: IpAddr = s.parse().map_err(|e| format!("invalid decap IP '{}': {}", s, e))?;
                Ok(ip_to_bytes(addr))
            })
            .collect();

        Ok(Self {
            vs: vs?,
            source_address_v4: source_v4.octets().to_vec(),
            source_address_v6: source_v6.octets().to_vec(),
            decap_addresses: decap?,
            sessions_timeouts: Some(config.sessions_timeouts.into()),
        })
    }
}

impl TryFrom<VirtualService> for balancerpb::VirtualService {
    type Error = String;

    fn try_from(vs: VirtualService) -> Result<Self, Self::Error> {
        let addr: IpAddr = vs.addr.parse().map_err(|e| format!("invalid VS IP: {}", e))?;
        let proto = match vs.proto {
            Proto::Tcp => balancerpb::TransportProto::Tcp,
            Proto::Udp => balancerpb::TransportProto::Udp,
        };
        let scheduler = match vs.scheduler {
            Scheduler::SourceHash => balancerpb::VsScheduler::SourceHash,
            Scheduler::RoundRobin => balancerpb::VsScheduler::RoundRobin,
        };

        let allowed_srcs: Result<Vec<_>, String> = vs
            .allowed_srcs
            .iter()
            .map(|entry| match entry {
                AllowedSrcEntry::Simple(network_str) => {
                    let (addr, mask) = parse_network(network_str)?;
                    Ok(balancerpb::AllowedSources {
                        nets: vec![filterpb::IpNet { addr: ip_to_bytes(addr), mask }],
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
                        nets: vec![filterpb::IpNet { addr: ip_to_bytes(addr), mask }],
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
            allowed_srcs: allowed_srcs?,
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
            ops: f.ops,
            pure_l3: f.pure_l3,
            wlc: f.wlc,
        }
    }
}

impl TryFrom<Real> for balancerpb::Real {
    type Error = String;

    fn try_from(real: Real) -> Result<Self, Self::Error> {
        let ip: IpAddr = real.ip.parse().map_err(|e| format!("invalid real IP: {}", e))?;
        let src_addr: IpAddr = real.src_addr.parse().map_err(|e| format!("invalid src_addr: {}", e))?;
        let src_mask: IpAddr = real.src_mask.parse().map_err(|e| format!("invalid src_mask: {}", e))?;

        Ok(Self {
            id: Some(balancerpb::RelativeRealIdentifier { ip: ip_to_bytes(ip), port: real.port }),
            weight: real.weight,
            src: Some(filterpb::IpNet {
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
            default: t.default,
        }
    }
}

impl From<StateConfig> for balancerpb::StateConfig {
    fn from(config: StateConfig) -> Self {
        Self {
            session_table_capacity: config.session_table.as_ref().map(|st| st.capacity),
            session_table_max_load_factor: config.session_table.as_ref().map(|st| st.max_load_factor),
            wlc: config.wlc.map(Into::into),
            refresh_period: config.refresh_period_ms.map(|ms| prost_types::Duration {
                seconds: (ms / 1000) as i64,
                nanos: ((ms % 1000) * 1_000_000) as i32,
            }),
        }
    }
}

impl From<WlcConfig> for balancerpb::WlcConfig {
    fn from(c: WlcConfig) -> Self {
        Self {
            power: c.power,
            max_weight: c.max_weight,
        }
    }
}
