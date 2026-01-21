//! Data structures and helper functions for balancer entities

use std::net::{IpAddr, Ipv4Addr, Ipv6Addr};

use serde::{Deserialize, Serialize};

use crate::rpc::balancerpb;

////////////////////////////////////////////////////////////////////////////////
// Helper Functions
////////////////////////////////////////////////////////////////////////////////

/// Convert IP address to bytes
pub fn ip_to_bytes(ip: IpAddr) -> Vec<u8> {
    match ip {
        IpAddr::V4(ipv4) => ipv4.octets().to_vec(),
        IpAddr::V6(ipv6) => ipv6.octets().to_vec(),
    }
}

/// Convert bytes to IP address
pub fn bytes_to_ip(bytes: &[u8]) -> Result<IpAddr, String> {
    match bytes.len() {
        4 => {
            let arr: [u8; 4] = bytes.try_into().map_err(|_| "invalid IPv4 bytes")?;
            Ok(IpAddr::V4(Ipv4Addr::from(arr)))
        }
        16 => {
            let arr: [u8; 16] = bytes.try_into().map_err(|_| "invalid IPv6 bytes")?;
            Ok(IpAddr::V6(Ipv6Addr::from(arr)))
        }
        _ => Err(format!("invalid IP address length: {}", bytes.len())),
    }
}

/// Convert Addr protobuf message to IP address
pub fn addr_to_ip(addr: &balancerpb::Addr) -> Result<IpAddr, String> {
    bytes_to_ip(&addr.bytes)
}

/// Convert optional Addr protobuf message to IP address
pub fn opt_addr_to_ip(addr: &Option<balancerpb::Addr>) -> Result<IpAddr, String> {
    addr.as_ref()
        .ok_or_else(|| "missing address".to_string())
        .and_then(addr_to_ip)
}

/// Parse CIDR notation (e.g., "192.168.0.0/24")
pub fn parse_cidr(cidr: &str) -> Result<(IpAddr, u32), String> {
    let parts: Vec<&str> = cidr.split('/').collect();
    if parts.len() != 2 {
        return Err(format!(
            "invalid CIDR format: '{}'. Expected format: '192.168.0.0/24'",
            cidr
        ));
    }

    let addr: IpAddr = parts[0]
        .parse()
        .map_err(|e| format!("invalid IP address in CIDR '{}': {}", cidr, e))?;
    let size: u32 = parts[1]
        .parse()
        .map_err(|e| format!("invalid prefix length in CIDR '{}': {}", cidr, e))?;

    // Validate prefix length
    match addr {
        IpAddr::V4(_) if size > 32 => {
            return Err(format!("invalid IPv4 prefix length: {} (max 32)", size));
        }
        IpAddr::V6(_) if size > 128 => {
            return Err(format!("invalid IPv6 prefix length: {} (max 128)", size));
        }
        _ => {}
    }

    Ok((addr, size))
}

/// Format bytes as human-readable size
pub fn format_bytes(bytes: u64) -> String {
    const UNITS: &[&str] = &["B", "KB", "MB", "GB", "TB"];
    let mut size = bytes as f64;
    let mut unit_idx = 0;

    while size >= 1024.0 && unit_idx < UNITS.len() - 1 {
        size /= 1024.0;
        unit_idx += 1;
    }

    if unit_idx == 0 {
        format!("{} {}", bytes, UNITS[unit_idx])
    } else {
        format!("{:.1} {}", size, UNITS[unit_idx])
    }
}

/// Format number with thousands separators
pub fn format_number(n: u64) -> String {
    let s = n.to_string();
    let mut result = String::new();

    for (count, c) in s.chars().rev().enumerate() {
        if count > 0 && count % 3 == 0 {
            result.push(',');
        }
        result.push(c);
    }

    result.chars().rev().collect()
}

////////////////////////////////////////////////////////////////////////////////
// Configuration Structures
////////////////////////////////////////////////////////////////////////////////

/// Complete balancer configuration for YAML
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BalancerConfig {
    /// Packet processing configuration (optional for UPDATE)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub packet_handler: Option<PacketHandlerConfig>,

    /// State management configuration (optional for UPDATE)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state: Option<StateConfig>,
}

impl BalancerConfig {
    /// Load configuration from a YAML file
    pub fn from_yaml_file(path: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let file = std::fs::File::open(path)?;
        let config = serde_yaml::from_reader(file)?;
        Ok(config)
    }
}

/// Packet processing configuration
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PacketHandlerConfig {
    pub vs: Vec<VirtualService>,
    pub source_address_v4: String,
    pub source_address_v6: String,
    pub decap_addresses: Vec<String>,
    pub sessions_timeouts: SessionsTimeouts,
}

/// Virtual service configuration (flat structure in YAML)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct VirtualService {
    // Flat fields (no nested 'id')
    pub addr: String,
    pub port: u32,
    pub proto: Proto,

    pub scheduler: Scheduler,
    pub flags: VsFlags,

    /// Allowed source networks in CIDR notation
    /// Empty list = allow NONE (reject all)
    /// ["0.0.0.0/0"] = allow all IPv4
    /// ["::/0"] = allow all IPv6
    #[serde(default)]
    pub allowed_srcs: Vec<String>,

    pub reals: Vec<Real>,
    #[serde(default)]
    pub peers: Vec<String>,
}

/// Real server configuration (flat structure in YAML)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Real {
    // Flat fields (no nested 'id')
    pub ip: String,
    pub port: u32, // Reserved for future use

    pub weight: u32,
    pub src_addr: String,
    pub src_mask: String,
}

/// Scheduler algorithm with flexible parsing
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
                "invalid scheduler: '{}'. Expected: SOURCE_HASH, source_hash, SH, sh, ROUND_ROBIN, round_robin, RR, rr",
                s
            ))),
        }
    }
}

/// Protocol with flexible parsing
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
                "invalid protocol: '{}'. Expected: TCP, tcp, UDP, udp",
                s
            ))),
        }
    }
}

/// Virtual service flags
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
    pub wlc: bool, // Dynamic weight adjustment flag
}

/// Session timeouts
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionsTimeouts {
    pub tcp_syn_ack: u32,
    pub tcp_syn: u32,
    pub tcp_fin: u32,
    pub tcp: u32,
    pub udp: u32,
    pub default: u32,
}

/// State management configuration
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct StateConfig {
    /// Session table configuration (optional in UPDATE)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub session_table: Option<SessionTableConfig>,

    /// WLC configuration (optional in UPDATE)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub wlc: Option<WlcConfig>,

    /// Refresh period in milliseconds (optional in UPDATE)
    /// Set to 0 to disable periodic refresh
    #[serde(skip_serializing_if = "Option::is_none")]
    pub refresh_period_ms: Option<u64>,
}

/// Session table configuration
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionTableConfig {
    pub capacity: u64,
    pub max_load_factor: f32,
}

/// WLC configuration
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WlcConfig {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub power: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_weight: Option<u32>,
}

////////////////////////////////////////////////////////////////////////////////
// Conversion to protobuf
////////////////////////////////////////////////////////////////////////////////

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
        let virtual_services: Result<Vec<_>, String> = config.vs.into_iter().map(TryInto::try_into).collect();

        let source_v4: Ipv4Addr = config
            .source_address_v4
            .parse()
            .map_err(|e| format!("invalid source IPv4 '{}': {}", config.source_address_v4, e))?;
        let source_v6: Ipv6Addr = config
            .source_address_v6
            .parse()
            .map_err(|e| format!("invalid source IPv6 '{}': {}", config.source_address_v6, e))?;

        let decap: Result<Vec<_>, String> = config
            .decap_addresses
            .into_iter()
            .map(|s| {
                let addr: IpAddr = s.parse().map_err(|e| format!("invalid decap IP '{}': {}", s, e))?;
                Ok(balancerpb::Addr { bytes: ip_to_bytes(addr) })
            })
            .collect();

        Ok(Self {
            vs: virtual_services?,
            source_address_v4: Some(balancerpb::Addr { bytes: source_v4.octets().to_vec() }),
            source_address_v6: Some(balancerpb::Addr { bytes: source_v6.octets().to_vec() }),
            decap_addresses: decap?,
            sessions_timeouts: Some(config.sessions_timeouts.into()),
        })
    }
}

impl TryFrom<VirtualService> for balancerpb::VirtualService {
    type Error = String;

    fn try_from(vs: VirtualService) -> Result<Self, Self::Error> {
        let addr: IpAddr = vs
            .addr
            .parse()
            .map_err(|e| format!("invalid VS IP address '{}': {}", vs.addr, e))?;

        let proto = match vs.proto {
            Proto::Tcp => balancerpb::TransportProto::Tcp,
            Proto::Udp => balancerpb::TransportProto::Udp,
        };

        let scheduler = match vs.scheduler {
            Scheduler::SourceHash => balancerpb::VsScheduler::SourceHash,
            Scheduler::RoundRobin => balancerpb::VsScheduler::RoundRobin,
        };

        // Parse CIDR notation for allowed sources
        let allowed_srcs: Result<Vec<_>, String> = vs
            .allowed_srcs
            .iter()
            .map(|cidr| {
                let (addr, size) = parse_cidr(cidr)?;
                Ok(balancerpb::Net {
                    addr: Some(balancerpb::Addr { bytes: ip_to_bytes(addr) }),
                    size,
                })
            })
            .collect();

        let peers: Result<Vec<_>, String> = vs
            .peers
            .iter()
            .map(|p| {
                let ip: IpAddr = p.parse().map_err(|e| format!("invalid peer IP '{}': {}", p, e))?;
                Ok(balancerpb::Addr { bytes: ip_to_bytes(ip) })
            })
            .collect();

        let reals: Result<Vec<_>, String> = vs.reals.into_iter().map(TryInto::try_into).collect();

        // Create VsIdentifier from flat fields
        let id = Some(balancerpb::VsIdentifier {
            addr: Some(balancerpb::Addr { bytes: ip_to_bytes(addr) }),
            port: vs.port,
            proto: proto as i32,
        });

        Ok(Self {
            id,
            scheduler: scheduler as i32,
            allowed_srcs: allowed_srcs?,
            reals: reals?,
            flags: Some(vs.flags.into()),
            peers: peers?,
        })
    }
}

impl From<VsFlags> for balancerpb::VsFlags {
    fn from(flags: VsFlags) -> Self {
        Self {
            gre: flags.gre,
            fix_mss: flags.fix_mss,
            ops: flags.ops,
            pure_l3: flags.pure_l3,
            wlc: flags.wlc,
        }
    }
}

impl TryFrom<Real> for balancerpb::Real {
    type Error = String;

    fn try_from(real: Real) -> Result<Self, Self::Error> {
        let ip: IpAddr = real
            .ip
            .parse()
            .map_err(|e| format!("invalid real IP '{}': {}", real.ip, e))?;
        let src_addr: IpAddr = real
            .src_addr
            .parse()
            .map_err(|e| format!("invalid src address '{}': {}", real.src_addr, e))?;
        let src_mask: IpAddr = real
            .src_mask
            .parse()
            .map_err(|e| format!("invalid src mask '{}': {}", real.src_mask, e))?;

        // Create RelativeRealIdentifier from flat fields
        let id = Some(balancerpb::RelativeRealIdentifier {
            ip: Some(balancerpb::Addr { bytes: ip_to_bytes(ip) }),
            port: real.port,
        });

        Ok(Self {
            id,
            weight: real.weight,
            src_addr: Some(balancerpb::Addr { bytes: ip_to_bytes(src_addr) }),
            src_mask: Some(balancerpb::Addr { bytes: ip_to_bytes(src_mask) }),
        })
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
            default: timeouts.default,
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
    fn from(config: WlcConfig) -> Self {
        Self {
            power: config.power,
            max_weight: config.max_weight,
        }
    }
}
