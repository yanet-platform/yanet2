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

/// Format AllowedSrc protobuf message as a string
/// Returns network in CIDR notation (if possible) or netmask notation with optional port ranges
/// Format: "10.0.0.0/8 [80,443,1024-65535]" or "10.0.0.0/255.0.255.0 [1024-65535]"
pub fn format_allowed_src(allowed_src: &balancerpb::AllowedSrc) -> Result<String, String> {
    let net = allowed_src.net.as_ref().ok_or("missing network")?;
    let addr = opt_addr_to_ip(&net.addr)?;
    let mask_bytes = net.mask.as_ref().ok_or("missing mask")?.bytes.as_slice();

    // Try to convert to CIDR prefix, fall back to netmask notation if not contiguous
    let network_str = match mask_to_prefix(mask_bytes) {
        Ok(prefix_len) => format!("{}/{}", addr, prefix_len),
        Err(_) => {
            // Non-contiguous mask - display as netmask notation
            let mask_ip = bytes_to_ip(mask_bytes)?;
            format!("{}/{}", addr, mask_ip)
        }
    };

    let mut result = network_str;

    // Add port ranges if present in square brackets
    if !allowed_src.ports.is_empty() {
        let port_strs: Vec<String> = allowed_src
            .ports
            .iter()
            .map(|pr| {
                if pr.from == pr.to {
                    format!("{}", pr.from)
                } else {
                    format!("{}-{}", pr.from, pr.to)
                }
            })
            .collect();
        result.push_str(&format!(" [{}]", port_strs.join(",")));
    }

    Ok(result)
}

/// Parse CIDR notation (e.g., "192.168.0.0/24")
/// This function is kept for backward compatibility but internally uses parse_network
#[allow(dead_code)]
pub fn parse_cidr(cidr: &str) -> Result<(IpAddr, u32), String> {
    let (addr, mask_bytes) = parse_network(cidr)?;

    // Convert mask bytes back to prefix length for backward compatibility
    let prefix_len = mask_to_prefix(&mask_bytes)?;
    Ok((addr, prefix_len))
}

/// Parse network specification in either CIDR or netmask notation
/// Returns (address, mask_bytes) where mask_bytes is the actual netmask
///
/// Examples:
/// - CIDR: "10.0.0.0/8" -> (10.0.0.0, [255, 0, 0, 0])
/// - Netmask: "10.0.0.0/255.0.0.0" -> (10.0.0.0, [255, 0, 0, 0])
pub fn parse_network(network: &str) -> Result<(IpAddr, Vec<u8>), String> {
    let parts: Vec<&str> = network.split('/').collect();
    if parts.len() != 2 {
        return Err(format!(
            "invalid network format: '{}'. Expected format: '192.168.0.0/24' or '192.168.0.0/255.255.0.0'",
            network
        ));
    }

    let addr: IpAddr = parts[0]
        .parse()
        .map_err(|e| format!("invalid IP address in network '{}': {}", network, e))?;

    let mask_part = parts[1];

    // Try to parse as CIDR prefix length first
    if let Ok(prefix_len) = mask_part.parse::<u32>() {
        // CIDR notation (e.g., "10.0.0.0/8")
        let max_prefix = match addr {
            IpAddr::V4(_) => 32,
            IpAddr::V6(_) => 128,
        };

        if prefix_len > max_prefix {
            return Err(format!(
                "invalid prefix length: {} (max {} for {})",
                prefix_len,
                max_prefix,
                if addr.is_ipv4() { "IPv4" } else { "IPv6" }
            ));
        }

        // Convert prefix length to netmask bytes
        let mask_bytes = prefix_to_mask(addr.is_ipv4(), prefix_len)?;
        Ok((addr, mask_bytes))
    } else {
        // Try to parse as netmask (e.g., "10.0.0.0/255.0.0.0")
        let mask: IpAddr = mask_part
            .parse()
            .map_err(|e| format!("invalid netmask in network '{}': {}", network, e))?;

        // Validate that address and mask are same IP version
        match (addr, mask) {
            (IpAddr::V4(_), IpAddr::V4(mask_v4)) => Ok((addr, mask_v4.octets().to_vec())),
            (IpAddr::V6(_), IpAddr::V6(mask_v6)) => Ok((addr, mask_v6.octets().to_vec())),
            _ => Err(format!(
                "IP version mismatch: address is {} but mask is {}",
                if addr.is_ipv4() { "IPv4" } else { "IPv6" },
                if mask.is_ipv4() { "IPv4" } else { "IPv6" }
            )),
        }
    }
}

/// Convert CIDR prefix length to netmask bytes
fn prefix_to_mask(is_ipv4: bool, prefix_len: u32) -> Result<Vec<u8>, String> {
    if is_ipv4 {
        // IPv4: 32 bits
        if prefix_len > 32 {
            return Err(format!("invalid IPv4 prefix length: {}", prefix_len));
        }

        let mask: u32 = if prefix_len == 0 { 0 } else { !0u32 << (32 - prefix_len) };

        Ok(mask.to_be_bytes().to_vec())
    } else {
        // IPv6: 128 bits
        if prefix_len > 128 {
            return Err(format!("invalid IPv6 prefix length: {}", prefix_len));
        }

        let mut mask = [0u8; 16];
        let full_bytes = (prefix_len / 8) as usize;
        let remaining_bits = prefix_len % 8;

        // Fill complete bytes with 0xFF
        for byte in mask.iter_mut().take(full_bytes) {
            *byte = 0xFF;
        }

        // Fill partial byte
        if remaining_bits > 0 && full_bytes < 16 {
            mask[full_bytes] = !0u8 << (8 - remaining_bits);
        }

        Ok(mask.to_vec())
    }
}

/// Convert netmask bytes to prefix length
pub fn mask_to_prefix(mask_bytes: &[u8]) -> Result<u32, String> {
    let mut prefix_len = 0u32;
    let mut seen_zero = false;

    for &byte in mask_bytes {
        if seen_zero {
            // After seeing a zero bit, all remaining bits must be zero
            if byte != 0 {
                return Err("invalid netmask: non-contiguous mask bits".to_string());
            }
        } else {
            // Count leading ones in this byte
            let leading_ones = byte.leading_ones();
            prefix_len += leading_ones;

            if leading_ones < 8 {
                // Found first zero bit
                seen_zero = true;

                // Verify remaining bits in this byte are zero
                let remaining_bits = byte & (!0u8 >> leading_ones);
                if remaining_bits != 0 {
                    return Err("invalid netmask: non-contiguous mask bits".to_string());
                }
            }
        }
    }

    Ok(prefix_len)
}

/// Parse port specification string into PortRange vector
/// Format: "80,443,8000-9000,1024-1030"
///
/// Examples:
/// - Single port: "80" -> [PortRange { from: 80, to: 80 }]
/// - Range: "1024-65535" -> [PortRange { from: 1024, to: 65535 }]
/// - Multiple: "80,443,8000-9000" -> [PortRange { from: 80, to: 80 }, PortRange { from: 443, to: 443 }, PortRange { from: 8000, to: 9000 }]
pub fn parse_ports(ports_str: &str) -> Result<Vec<PortRange>, String> {
    let mut ranges = Vec::new();

    for part in ports_str.split(',') {
        let part = part.trim();
        if part.is_empty() {
            continue;
        }

        if part.contains('-') {
            // Parse range "1024-65535"
            let parts: Vec<&str> = part.split('-').collect();
            if parts.len() != 2 {
                return Err(format!("invalid port range: '{}'. Expected format: 'from-to'", part));
            }

            let from: u32 = parts[0]
                .trim()
                .parse()
                .map_err(|e| format!("invalid port '{}': {}", parts[0], e))?;
            let to: u32 = parts[1]
                .trim()
                .parse()
                .map_err(|e| format!("invalid port '{}': {}", parts[1], e))?;

            if !(1..=65535).contains(&from) {
                return Err(format!("port {} out of range (1-65535)", from));
            }
            if !(1..=65535).contains(&to) {
                return Err(format!("port {} out of range (1-65535)", to));
            }
            if from > to {
                return Err(format!("invalid range {}-{}: from > to", from, to));
            }

            ranges.push(PortRange { from, to });
        } else {
            // Parse single port "80"
            let port: u32 = part.parse().map_err(|e| format!("invalid port '{}': {}", part, e))?;

            if !(1..=65535).contains(&port) {
                return Err(format!("port {} out of range (1-65535)", port));
            }

            ranges.push(PortRange { from: port, to: port });
        }
    }

    if ranges.is_empty() {
        return Err("no valid port ranges specified".to_string());
    }

    Ok(ranges)
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

/// Port range for source filtering
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PortRange {
    pub from: u32,
    pub to: u32,
}

/// Allowed source entry - supports both simple string and structured format
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum AllowedSrcEntry {
    /// Simple network string (backward compatible)
    /// Supports both CIDR ("10.0.0.0/8") and netmask ("10.0.0.0/255.0.0.0") notation
    Simple(String),

    /// Structured format with optional port restrictions
    Structured {
        /// Network in CIDR or netmask notation
        network: String,

        /// Optional port restrictions (comma-separated ranges)
        /// Format: "80,443,8000-9000,1024-1030"
        #[serde(skip_serializing_if = "Option::is_none")]
        ports: Option<String>,
    },
}

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

    /// Allowed source networks with optional port restrictions
    /// Supports both simple CIDR strings and structured format with ports
    /// Empty list = allow NONE (reject all)
    /// ["0.0.0.0/0"] = allow all IPv4
    /// ["::/0"] = allow all IPv6
    #[serde(default)]
    pub allowed_srcs: Vec<AllowedSrcEntry>,

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

        Ok(Self {
            vs: virtual_services?,
            source_address_v4: Some(balancerpb::Addr { bytes: source_v4.octets().to_vec() }),
            source_address_v6: Some(balancerpb::Addr { bytes: source_v6.octets().to_vec() }),
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

        // Parse allowed sources with optional port ranges
        let allowed_srcs: Result<Vec<_>, String> = vs
            .allowed_srcs
            .iter()
            .map(|entry| {
                match entry {
                    AllowedSrcEntry::Simple(network_str) => {
                        // Simple format - no port restrictions
                        let (addr, mask_bytes) = parse_network(network_str)?;
                        Ok(balancerpb::AllowedSrc {
                            net: Some(balancerpb::Net {
                                addr: Some(balancerpb::Addr { bytes: ip_to_bytes(addr) }),
                                mask: Some(balancerpb::Addr { bytes: mask_bytes }),
                            }),
                            ports: vec![], // Empty = all ports allowed
                        })
                    }
                    AllowedSrcEntry::Structured { network, ports } => {
                        // Structured format with optional ports
                        let (addr, mask_bytes) = parse_network(network)?;

                        let port_ranges = if let Some(ports_str) = ports {
                            parse_ports(ports_str)?
                                .into_iter()
                                .map(|pr| balancerpb::PortsRange { from: pr.from, to: pr.to })
                                .collect()
                        } else {
                            vec![] // No ports specified = all ports allowed
                        };

                        Ok(balancerpb::AllowedSrc {
                            net: Some(balancerpb::Net {
                                addr: Some(balancerpb::Addr { bytes: ip_to_bytes(addr) }),
                                mask: Some(balancerpb::Addr { bytes: mask_bytes }),
                            }),
                            ports: port_ranges,
                        })
                    }
                }
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
