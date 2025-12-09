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

/// Parse subnet string (e.g., "192.0.2.0/24")
pub fn parse_subnet(s: &str) -> Result<balancerpb::Subnet, String> {
    let parts: Vec<&str> = s.split('/').collect();
    if parts.len() != 2 {
        return Err(format!("invalid subnet format: {}", s));
    }

    let addr: IpAddr = parts[0]
        .parse()
        .map_err(|e| format!("invalid subnet address: {}", e))?;
    let size: u32 = parts[1]
        .parse()
        .map_err(|e| format!("invalid subnet size: {}", e))?;

    Ok(balancerpb::Subnet {
        addr: ip_to_bytes(addr),
        size,
    })
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
    pub module_config: ModuleConfig,
    pub module_state_config: ModuleStateConfig,
}

impl BalancerConfig {
    /// Load configuration from a YAML file
    pub fn from_yaml_file(path: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let file = std::fs::File::open(path)?;
        let config = serde_yaml::from_reader(file)?;
        Ok(config)
    }
}

/// Module configuration
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ModuleConfig {
    pub virtual_services: Vec<VirtualService>,
    pub source_address_v4: String,
    pub source_address_v6: String,
    #[serde(default)]
    pub decap_addresses: Vec<String>,
    #[serde(default)]
    pub sessions_timeouts: SessionsTimeouts,
    #[serde(default)]
    pub wlc: WlcConfig,
}

/// Module state configuration
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ModuleStateConfig {
    pub session_table_capacity: u64,
    pub session_table_scan_period_ms: u64,
    pub session_table_max_load_factor: f32,
}

/// Virtual service configuration
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct VirtualService {
    pub ip: String,
    pub port: u16,
    pub proto: String,
    pub scheduler: String,
    #[serde(default)]
    pub flags: VsFlags,
    #[serde(default)]
    pub allowed_srcs: Vec<String>,
    pub reals: Vec<Real>,
    #[serde(default)]
    pub peers: Vec<String>,
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
}

/// Real server configuration
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Real {
    pub weight: u32,
    pub dst: String,
    pub src: String,
    pub src_mask: String,
    #[serde(default = "default_true")]
    pub enabled: bool,
}

fn default_true() -> bool {
    true
}

/// Session timeouts
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionsTimeouts {
    #[serde(default = "default_timeout")]
    pub tcp_syn_ack: u32,
    #[serde(default = "default_timeout")]
    pub tcp_syn: u32,
    #[serde(default = "default_timeout")]
    pub tcp_fin: u32,
    #[serde(default = "default_tcp_timeout")]
    pub tcp: u32,
    #[serde(default = "default_udp_timeout")]
    pub udp: u32,
    #[serde(default = "default_default_timeout")]
    pub default: u32,
}

fn default_timeout() -> u32 { 10 }
fn default_tcp_timeout() -> u32 { 60 }
fn default_udp_timeout() -> u32 { 30 }
fn default_default_timeout() -> u32 { 60 }

impl Default for SessionsTimeouts {
    fn default() -> Self {
        Self {
            tcp_syn_ack: default_timeout(),
            tcp_syn: default_timeout(),
            tcp_fin: default_timeout(),
            tcp: default_tcp_timeout(),
            udp: default_udp_timeout(),
            default: default_default_timeout(),
        }
    }
}

/// WLC configuration
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WlcConfig {
    #[serde(default = "default_wlc_power")]
    pub power: u64,
    #[serde(default = "default_max_real_weight")]
    pub max_real_weight: u32,
    #[serde(default = "default_update_period_ms")]
    pub update_period_ms: u64,
}

fn default_wlc_power() -> u64 { 10 }
fn default_max_real_weight() -> u32 { 1000 }
fn default_update_period_ms() -> u64 { 5000 }

impl Default for WlcConfig {
    fn default() -> Self {
        Self {
            power: default_wlc_power(),
            max_real_weight: default_max_real_weight(),
            update_period_ms: default_update_period_ms(),
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// Conversion to protobuf
////////////////////////////////////////////////////////////////////////////////

impl TryFrom<BalancerConfig> for (balancerpb::ModuleConfig, balancerpb::ModuleStateConfig) {
    type Error = String;

    fn try_from(config: BalancerConfig) -> Result<Self, Self::Error> {
        let module_config = config.module_config.try_into()?;
        let module_state_config = config.module_state_config.into();
        Ok((module_config, module_state_config))
    }
}

impl TryFrom<ModuleConfig> for balancerpb::ModuleConfig {
    type Error = String;

    fn try_from(config: ModuleConfig) -> Result<Self, Self::Error> {
        let virtual_services: Result<Vec<_>, String> = config
            .virtual_services
            .into_iter()
            .map(TryInto::try_into)
            .collect();

        let source_v4: Ipv4Addr = config.source_address_v4
            .parse()
            .map_err(|e| format!("invalid IPv4: {}", e))?;
        let source_v6: Ipv6Addr = config.source_address_v6
            .parse()
            .map_err(|e| format!("invalid IPv6: {}", e))?;

        let decap: Result<Vec<_>, String> = config
            .decap_addresses
            .into_iter()
            .map(|s| {
                let addr: IpAddr = s.parse().map_err(|e| format!("invalid decap IP: {}", e))?;
                Ok(ip_to_bytes(addr))
            })
            .collect();

        Ok(Self {
            virtual_services: virtual_services?,
            source_address_v4: source_v4.octets().to_vec(),
            source_address_v6: source_v6.octets().to_vec(),
            decap_addresses: decap?,
            sessions_timeouts: Some(config.sessions_timeouts.into()),
            wlc: Some(config.wlc.into()),
        })
    }
}

impl From<ModuleStateConfig> for balancerpb::ModuleStateConfig {
    fn from(config: ModuleStateConfig) -> Self {
        Self {
            session_table_capacity: config.session_table_capacity,
            session_table_scan_period: Some(prost_types::Duration {
                seconds: (config.session_table_scan_period_ms / 1000) as i64,
                nanos: ((config.session_table_scan_period_ms % 1000) * 1_000_000) as i32,
            }),
            session_table_max_load_factor: config.session_table_max_load_factor,
        }
    }
}

impl TryFrom<VirtualService> for balancerpb::VirtualService {
    type Error = String;

    fn try_from(vs: VirtualService) -> Result<Self, Self::Error> {
        let addr: IpAddr = vs.ip.parse().map_err(|e| format!("invalid IP: {}", e))?;
        
        let proto = match vs.proto.to_lowercase().as_str() {
            "tcp" => balancerpb::TransportProto::Tcp,
            "udp" => balancerpb::TransportProto::Udp,
            _ => return Err(format!("invalid proto: {}", vs.proto)),
        };

        let scheduler = match vs.scheduler.to_lowercase().as_str() {
            "wrr" => balancerpb::VsScheduler::Wrr,
            "prr" => balancerpb::VsScheduler::Prr,
            "wlc" => balancerpb::VsScheduler::Wlc,
            _ => return Err(format!("invalid scheduler: {}", vs.scheduler)),
        };

        let allowed_srcs: Result<Vec<_>, String> = vs
            .allowed_srcs
            .into_iter()
            .map(|s| parse_subnet(&s))
            .collect();

        let peers: Result<Vec<_>, String> = vs
            .peers
            .into_iter()
            .map(|p| {
                let addr: IpAddr = p.parse().map_err(|e| format!("invalid peer IP: {}", e))?;
                Ok(ip_to_bytes(addr))
            })
            .collect();

        let reals: Vec<_> = vs.reals.into_iter().map(Into::into).collect();

        Ok(Self {
            addr: ip_to_bytes(addr),
            port: vs.port as u32,
            proto: proto as i32,
            scheduler: scheduler as i32,
            allowed_srcs: allowed_srcs?,
            reals,
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
        }
    }
}

impl From<Real> for balancerpb::Real {
    fn from(real: Real) -> Self {
        let dst_addr: IpAddr = real.dst.parse().expect("invalid dst address");
        let src_addr: IpAddr = real.src.parse().expect("invalid src address");
        let src_mask: IpAddr = real.src_mask.parse().expect("invalid src mask");

        Self {
            weight: real.weight,
            dst_addr: ip_to_bytes(dst_addr),
            src_addr: ip_to_bytes(src_addr),
            src_mask: ip_to_bytes(src_mask),
            enabled: real.enabled,
            port: 0, // Not used
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
            default: timeouts.default,
        }
    }
}

impl From<WlcConfig> for balancerpb::WlcConfig {
    fn from(config: WlcConfig) -> Self {
        Self {
            wlc_power: config.power,
            max_real_weight: config.max_real_weight,
            update_period: Some(prost_types::Duration {
                seconds: (config.update_period_ms / 1000) as i64,
                nanos: ((config.update_period_ms % 1000) * 1_000_000) as i32,
            }),
        }
    }
}