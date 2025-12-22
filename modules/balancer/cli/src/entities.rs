//! Data structures and helper functions for balancer entities

use crate::rpc::balancerpb;
use serde::{Deserialize, Serialize};
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr};

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

    let addr: IpAddr = parts[0].parse().map_err(|e| format!("invalid subnet address: {}", e))?;
    let size: u32 = parts[1].parse().map_err(|e| format!("invalid subnet size: {}", e))?;

    Ok(balancerpb::Subnet { addr: ip_to_bytes(addr), size })
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

fn default_timeout() -> u32 {
    10
}
fn default_tcp_timeout() -> u32 {
    60
}
fn default_udp_timeout() -> u32 {
    30
}
fn default_default_timeout() -> u32 {
    60
}

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

fn default_wlc_power() -> u64 {
    10
}
fn default_max_real_weight() -> u32 {
    1000
}
fn default_update_period_ms() -> u64 {
    5000
}

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
        let virtual_services: Result<Vec<_>, String> =
            config.virtual_services.into_iter().map(TryInto::try_into).collect();

        let source_v4: Ipv4Addr = config
            .source_address_v4
            .parse()
            .map_err(|e| format!("invalid IPv4: {}", e))?;
        let source_v6: Ipv6Addr = config
            .source_address_v6
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

        let allowed_srcs: Result<Vec<_>, String> = vs.allowed_srcs.into_iter().map(|s| parse_subnet(&s)).collect();

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

////////////////////////////////////////////////////////////////////////////////
// Tests
////////////////////////////////////////////////////////////////////////////////

#[cfg(test)]
mod tests {
    use super::*;

    const TEST_BALANCER_YAML: &str = r#"
# Example Balancer Configuration

# Module configuration
module_config:
  # Virtual services configuration
  virtual_services:
    # Example HTTP service
    - ip: "192.0.2.1"
      port: 80
      proto: tcp
      scheduler: wrr  # Options: wrr, prr, wlc
      flags:
        gre: false
        fix_mss: true
        ops: false
        pure_l3: false
      allowed_srcs:
        - "10.0.0.0/8"
        - "172.16.0.0/12"
      reals:
        - weight: 100
          dst: "10.1.1.1"
          src: "192.0.2.1"
          src_mask: "255.255.255.255"
          enabled: true
        - weight: 50
          dst: "10.1.1.2"
          src: "192.0.2.1"
          src_mask: "255.255.255.255"
          enabled: true
      peers:
        - "192.0.2.10"
        - "192.0.2.11"

    # Example HTTPS service with WLC scheduler
    - ip: "192.0.2.2"
      port: 443
      proto: tcp
      scheduler: wlc
      flags:
        gre: false
        fix_mss: true
        ops: false
        pure_l3: false
      allowed_srcs:
        - "0.0.0.0/0"  # Allow all sources
      reals:
        - weight: 100
          dst: "10.2.1.1"
          src: "192.0.2.2"
          src_mask: "255.255.255.255"
          enabled: true
        - weight: 100
          dst: "10.2.1.2"
          src: "192.0.2.2"
          src_mask: "255.255.255.255"
          enabled: true
        - weight: 50
          dst: "10.2.1.3"
          src: "192.0.2.2"
          src_mask: "255.255.255.255"
          enabled: false  # Disabled real
      peers:
        - "192.0.2.10"

    # Example UDP service
    - ip: "192.0.2.3"
      port: 53
      proto: udp
      scheduler: prr
      flags:
        gre: false
        fix_mss: false
        ops: true
        pure_l3: false
      allowed_srcs:
        - "10.0.0.0/8"
      reals:
        - weight: 1
          dst: "10.3.1.1"
          src: "192.0.2.3"
          src_mask: "255.255.255.255"
          enabled: true
        - weight: 1
          dst: "10.3.1.2"
          src: "192.0.2.3"
          src_mask: "255.255.255.255"
          enabled: true
      peers: []

  # Source addresses for the balancer
  source_address_v4: "192.0.2.1"
  source_address_v6: "2001:db8::1"

  # Addresses for decapsulation
  decap_addresses:
    - "192.0.2.1"
    - "2001:db8::1"

  # Session timeouts (in seconds)
  sessions_timeouts:
    tcp_syn_ack: 10
    tcp_syn: 10
    tcp_fin: 10
    tcp: 60
    udp: 30
    default: 60

  # Weighted Least Connections configuration
  wlc:
    power: 10
    max_real_weight: 1000
    update_period_ms: 5000

# Module state configuration
module_state_config:
  # Maximum number of sessions in the session table
  # (can be extended on demand automatically)
  session_table_capacity: 1000

  # Period to scan the session table (in milliseconds)
  session_table_scan_period_ms: 1000

  # Maximum load factor for the session table (0.0 to 1.0)
  session_table_max_load_factor: 0.75
"#;

    #[test]
    fn test_parse_balancer_config() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        // Verify module_config exists
        assert!(!config.module_config.virtual_services.is_empty());
        assert_eq!(config.module_config.virtual_services.len(), 3);
    }

    #[test]
    fn test_module_config_source_addresses() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        assert_eq!(config.module_config.source_address_v4, "192.0.2.1");
        assert_eq!(config.module_config.source_address_v6, "2001:db8::1");
    }

    #[test]
    fn test_module_config_decap_addresses() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        assert_eq!(config.module_config.decap_addresses.len(), 2);
        assert_eq!(config.module_config.decap_addresses[0], "192.0.2.1");
        assert_eq!(config.module_config.decap_addresses[1], "2001:db8::1");
    }

    #[test]
    fn test_virtual_service_http() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let vs = &config.module_config.virtual_services[0];
        assert_eq!(vs.ip, "192.0.2.1");
        assert_eq!(vs.port, 80);
        assert_eq!(vs.proto, "tcp");
        assert_eq!(vs.scheduler, "wrr");

        // Check flags
        assert!(!vs.flags.gre);
        assert!(vs.flags.fix_mss);
        assert!(!vs.flags.ops);
        assert!(!vs.flags.pure_l3);

        // Check allowed sources
        assert_eq!(vs.allowed_srcs.len(), 2);
        assert_eq!(vs.allowed_srcs[0], "10.0.0.0/8");
        assert_eq!(vs.allowed_srcs[1], "172.16.0.0/12");

        // Check peers
        assert_eq!(vs.peers.len(), 2);
        assert_eq!(vs.peers[0], "192.0.2.10");
        assert_eq!(vs.peers[1], "192.0.2.11");
    }

    #[test]
    fn test_virtual_service_https() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let vs = &config.module_config.virtual_services[1];
        assert_eq!(vs.ip, "192.0.2.2");
        assert_eq!(vs.port, 443);
        assert_eq!(vs.proto, "tcp");
        assert_eq!(vs.scheduler, "wlc");

        // Check allowed sources
        assert_eq!(vs.allowed_srcs.len(), 1);
        assert_eq!(vs.allowed_srcs[0], "0.0.0.0/0");

        // Check peers
        assert_eq!(vs.peers.len(), 1);
        assert_eq!(vs.peers[0], "192.0.2.10");
    }

    #[test]
    fn test_virtual_service_udp() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let vs = &config.module_config.virtual_services[2];
        assert_eq!(vs.ip, "192.0.2.3");
        assert_eq!(vs.port, 53);
        assert_eq!(vs.proto, "udp");
        assert_eq!(vs.scheduler, "prr");

        // Check flags
        assert!(!vs.flags.gre);
        assert!(!vs.flags.fix_mss);
        assert!(vs.flags.ops);
        assert!(!vs.flags.pure_l3);

        // Check peers (empty)
        assert_eq!(vs.peers.len(), 0);
    }

    #[test]
    fn test_real_servers_http() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let vs = &config.module_config.virtual_services[0];
        assert_eq!(vs.reals.len(), 2);

        // First real
        let real1 = &vs.reals[0];
        assert_eq!(real1.weight, 100);
        assert_eq!(real1.dst, "10.1.1.1");
        assert_eq!(real1.src, "192.0.2.1");
        assert_eq!(real1.src_mask, "255.255.255.255");
        assert!(real1.enabled);

        // Second real
        let real2 = &vs.reals[1];
        assert_eq!(real2.weight, 50);
        assert_eq!(real2.dst, "10.1.1.2");
        assert_eq!(real2.src, "192.0.2.1");
        assert_eq!(real2.src_mask, "255.255.255.255");
        assert!(real2.enabled);
    }

    #[test]
    fn test_real_servers_https_with_disabled() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let vs = &config.module_config.virtual_services[1];
        assert_eq!(vs.reals.len(), 3);

        // First two reals are enabled
        assert!(vs.reals[0].enabled);
        assert_eq!(vs.reals[0].weight, 100);
        assert_eq!(vs.reals[0].dst, "10.2.1.1");

        assert!(vs.reals[1].enabled);
        assert_eq!(vs.reals[1].weight, 100);
        assert_eq!(vs.reals[1].dst, "10.2.1.2");

        // Third real is disabled
        assert!(!vs.reals[2].enabled);
        assert_eq!(vs.reals[2].weight, 50);
        assert_eq!(vs.reals[2].dst, "10.2.1.3");
    }

    #[test]
    fn test_real_servers_udp() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let vs = &config.module_config.virtual_services[2];
        assert_eq!(vs.reals.len(), 2);

        // Both reals have weight 1
        assert_eq!(vs.reals[0].weight, 1);
        assert_eq!(vs.reals[0].dst, "10.3.1.1");
        assert!(vs.reals[0].enabled);

        assert_eq!(vs.reals[1].weight, 1);
        assert_eq!(vs.reals[1].dst, "10.3.1.2");
        assert!(vs.reals[1].enabled);
    }

    #[test]
    fn test_sessions_timeouts() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let timeouts = &config.module_config.sessions_timeouts;
        assert_eq!(timeouts.tcp_syn_ack, 10);
        assert_eq!(timeouts.tcp_syn, 10);
        assert_eq!(timeouts.tcp_fin, 10);
        assert_eq!(timeouts.tcp, 60);
        assert_eq!(timeouts.udp, 30);
        assert_eq!(timeouts.default, 60);
    }

    #[test]
    fn test_wlc_config() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let wlc = &config.module_config.wlc;
        assert_eq!(wlc.power, 10);
        assert_eq!(wlc.max_real_weight, 1000);
        assert_eq!(wlc.update_period_ms, 5000);
    }

    #[test]
    fn test_module_state_config() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let state_config = &config.module_state_config;
        assert_eq!(state_config.session_table_capacity, 1000);
        assert_eq!(state_config.session_table_scan_period_ms, 1000);
        assert_eq!(state_config.session_table_max_load_factor, 0.75);
    }

    #[test]
    fn test_config_conversion_to_protobuf() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let result: Result<(balancerpb::ModuleConfig, balancerpb::ModuleStateConfig), _> = config.try_into();
        assert!(result.is_ok(), "Failed to convert config to protobuf");

        let (module_config, module_state_config) = result.unwrap();

        // Verify module_config
        assert_eq!(module_config.virtual_services.len(), 3);
        assert_eq!(module_config.source_address_v4.len(), 4); // IPv4 is 4 bytes
        assert_eq!(module_config.source_address_v6.len(), 16); // IPv6 is 16 bytes
        assert_eq!(module_config.decap_addresses.len(), 2);
        assert!(module_config.sessions_timeouts.is_some());
        assert!(module_config.wlc.is_some());

        // Verify module_state_config
        assert_eq!(module_state_config.session_table_capacity, 1000);
        assert!(module_state_config.session_table_scan_period.is_some());
        assert_eq!(module_state_config.session_table_max_load_factor, 0.75);
    }

    #[test]
    fn test_protobuf_virtual_services() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let (module_config, _) = config.try_into().expect("Failed to convert to protobuf");

        // Check first VS (HTTP)
        let vs1 = &module_config.virtual_services[0];
        assert_eq!(vs1.port, 80);
        assert_eq!(vs1.proto, balancerpb::TransportProto::Tcp as i32);
        assert_eq!(vs1.scheduler, balancerpb::VsScheduler::Wrr as i32);
        assert_eq!(vs1.reals.len(), 2);
        assert_eq!(vs1.allowed_srcs.len(), 2);
        assert_eq!(vs1.peers.len(), 2);

        // Check second VS (HTTPS)
        let vs2 = &module_config.virtual_services[1];
        assert_eq!(vs2.port, 443);
        assert_eq!(vs2.proto, balancerpb::TransportProto::Tcp as i32);
        assert_eq!(vs2.scheduler, balancerpb::VsScheduler::Wlc as i32);
        assert_eq!(vs2.reals.len(), 3);

        // Check third VS (UDP)
        let vs3 = &module_config.virtual_services[2];
        assert_eq!(vs3.port, 53);
        assert_eq!(vs3.proto, balancerpb::TransportProto::Udp as i32);
        assert_eq!(vs3.scheduler, balancerpb::VsScheduler::Prr as i32);
        assert_eq!(vs3.reals.len(), 2);
    }

    #[test]
    fn test_protobuf_real_enabled_disabled() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let (module_config, _) = config.try_into().expect("Failed to convert to protobuf");

        // Check HTTPS service with disabled real
        let vs = &module_config.virtual_services[1];
        assert!(vs.reals[0].enabled); // First real is enabled
        assert!(vs.reals[1].enabled); // Second real is enabled
        assert!(!vs.reals[2].enabled); // Third real is disabled
    }

    #[test]
    fn test_protobuf_vs_flags() {
        let config: BalancerConfig = serde_yaml::from_str(TEST_BALANCER_YAML).expect("Failed to parse YAML");

        let (module_config, _) = config.try_into().expect("Failed to convert to protobuf");

        // Check HTTP service flags
        let vs1_flags = module_config.virtual_services[0].flags.as_ref().unwrap();
        assert!(!vs1_flags.gre);
        assert!(vs1_flags.fix_mss);
        assert!(!vs1_flags.ops);
        assert!(!vs1_flags.pure_l3);

        // Check UDP service flags
        let vs3_flags = module_config.virtual_services[2].flags.as_ref().unwrap();
        assert!(!vs3_flags.gre);
        assert!(!vs3_flags.fix_mss);
        assert!(vs3_flags.ops);
        assert!(!vs3_flags.pure_l3);
    }
}
