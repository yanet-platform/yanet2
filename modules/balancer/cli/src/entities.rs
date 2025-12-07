//! Balancer entities that mirror the Go controlplane structures.
//! These entities are used for parsing YAML configuration and converting to protobuf.

use std::fmt;
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr};

use serde::{Deserialize, Serialize};

use crate::rpc::balancerpb;

////////////////////////////////////////////////////////////////////////////////
// Transport Protocol
////////////////////////////////////////////////////////////////////////////////

/// Virtual service transport protocol (TCP or UDP).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Proto {
    Tcp,
    Udp,
}

impl Proto {
    pub fn as_str(&self) -> &'static str {
        match self {
            Proto::Tcp => "tcp",
            Proto::Udp => "udp",
        }
    }
}

impl fmt::Display for Proto {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

impl From<Proto> for balancerpb::TransportProto {
    fn from(proto: Proto) -> Self {
        match proto {
            Proto::Tcp => balancerpb::TransportProto::Tcp,
            Proto::Udp => balancerpb::TransportProto::Udp,
        }
    }
}

impl From<balancerpb::TransportProto> for Proto {
    fn from(proto: balancerpb::TransportProto) -> Self {
        match proto {
            balancerpb::TransportProto::Tcp => Proto::Tcp,
            balancerpb::TransportProto::Udp => Proto::Udp,
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// Virtual Service Flags
////////////////////////////////////////////////////////////////////////////////

/// Virtual service flags.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct VsFlags {
    /// Use GRE for encapsulation
    #[serde(default)]
    pub gre: bool,

    /// One packet scheduler
    #[serde(default)]
    pub ops: bool,

    /// Use pure L3 scheduling (listens to all ports)
    #[serde(default)]
    pub pure_l3: bool,

    /// Fix MSS TCP option
    #[serde(default)]
    pub fix_mss: bool,
}

impl Default for VsFlags {
    fn default() -> Self {
        Self {
            gre: false,
            ops: false,
            pure_l3: false,
            fix_mss: false,
        }
    }
}

impl fmt::Display for VsFlags {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let mut flags = Vec::new();
        if self.gre {
            flags.push("gre");
        }
        if self.ops {
            flags.push("ops");
        }
        if self.pure_l3 {
            flags.push("pure_l3");
        }
        if self.fix_mss {
            flags.push("fix_mss");
        }
        if flags.is_empty() {
            write!(f, "none")
        } else {
            write!(f, "{}", flags.join(", "))
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

impl From<balancerpb::VsFlags> for VsFlags {
    fn from(flags: balancerpb::VsFlags) -> Self {
        Self {
            gre: flags.gre,
            ops: flags.ops,
            pure_l3: flags.pure_l3,
            fix_mss: flags.fix_mss,
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// Scheduler
////////////////////////////////////////////////////////////////////////////////

/// Scheduler of the virtual service.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Scheduler {
    /// Weighted Round Robin - selects reals according to weight and 5-tuple hash
    Wrr,
    /// Pure Round Robin - selects reals according to weight and monotonic counter
    Prr,
    /// Weighted Least Connection - selects reals according to weight and connection count
    Wlc,
}

impl Scheduler {
    pub fn as_str(&self) -> &'static str {
        match self {
            Scheduler::Wrr => "wrr",
            Scheduler::Prr => "prr",
            Scheduler::Wlc => "wlc",
        }
    }
}

impl fmt::Display for Scheduler {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

impl From<Scheduler> for balancerpb::VsScheduler {
    fn from(sched: Scheduler) -> Self {
        match sched {
            Scheduler::Wrr => balancerpb::VsScheduler::Wrr,
            Scheduler::Prr => balancerpb::VsScheduler::Prr,
            Scheduler::Wlc => balancerpb::VsScheduler::Wlc,
        }
    }
}

impl From<balancerpb::VsScheduler> for Scheduler {
    fn from(sched: balancerpb::VsScheduler) -> Self {
        match sched {
            balancerpb::VsScheduler::Wrr => Scheduler::Wrr,
            balancerpb::VsScheduler::Prr => Scheduler::Prr,
            balancerpb::VsScheduler::Wlc => Scheduler::Wlc,
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// Real Server
////////////////////////////////////////////////////////////////////////////////

/// Real server of a virtual service.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Real {
    /// Weight of the real server
    pub weight: u16,

    /// Destination address of the real server
    pub dst: String,

    /// Source address for forwarding
    pub src: String,

    /// Source mask for forwarding
    pub src_mask: String,

    /// Whether the real is enabled
    #[serde(default = "default_true")]
    pub enabled: bool,
}

fn default_true() -> bool {
    true
}

impl fmt::Display for Real {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "dst={} weight={} src={} mask={} enabled={}",
            self.dst, self.weight, self.src, self.src_mask, self.enabled
        )
    }
}

impl From<Real> for balancerpb::Real {
    fn from(real: Real) -> Self {
        let dst_addr: IpAddr = real.dst.parse().expect("invalid dst address");
        let src_addr: IpAddr = real.src.parse().expect("invalid src address");
        let src_mask: IpAddr = real.src_mask.parse().expect("invalid src mask");

        Self {
            weight: real.weight as u32,
            dst_addr: ip_to_bytes(dst_addr),
            src_addr: ip_to_bytes(src_addr),
            src_mask: ip_to_bytes(src_mask),
            enabled: real.enabled,
        }
    }
}

impl TryFrom<balancerpb::Real> for Real {
    type Error = String;

    fn try_from(real: balancerpb::Real) -> Result<Self, Self::Error> {
        Ok(Self {
            weight: real.weight as u16,
            dst: bytes_to_ip(&real.dst_addr)?.to_string(),
            src: bytes_to_ip(&real.src_addr)?.to_string(),
            src_mask: bytes_to_ip(&real.src_mask)?.to_string(),
            enabled: real.enabled,
        })
    }
}

////////////////////////////////////////////////////////////////////////////////
// Virtual Service
////////////////////////////////////////////////////////////////////////////////

/// Virtual service configuration.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct VirtualService {
    /// Virtual IP address
    pub ip: String,

    /// Transport protocol
    pub proto: Proto,

    /// L4 port (0 for L3-only services)
    pub port: u16,

    /// Scheduler algorithm
    pub scheduler: Scheduler,

    /// Service flags
    #[serde(default)]
    pub flags: VsFlags,

    /// Allowed source networks
    #[serde(default)]
    pub allowed_srcs: Vec<String>,

    /// Real servers
    pub reals: Vec<Real>,

    /// Peer balancer addresses
    #[serde(default)]
    pub peers: Vec<String>,
}

impl fmt::Display for VirtualService {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        writeln!(f, "Virtual Service:")?;
        writeln!(f, "  Address: {}:{}", self.ip, self.port)?;
        writeln!(f, "  Protocol: {}", self.proto)?;
        writeln!(f, "  Scheduler: {}", self.scheduler)?;
        writeln!(f, "  Flags: {}", self.flags)?;
        
        if !self.allowed_srcs.is_empty() {
            writeln!(f, "  Allowed Sources:")?;
            for src in &self.allowed_srcs {
                writeln!(f, "    - {}", src)?;
            }
        }
        
        if !self.peers.is_empty() {
            writeln!(f, "  Peers:")?;
            for peer in &self.peers {
                writeln!(f, "    - {}", peer)?;
            }
        }
        
        writeln!(f, "  Reals ({}):", self.reals.len())?;
        for (i, real) in self.reals.iter().enumerate() {
            writeln!(f, "    [{}] {}", i, real)?;
        }
        
        Ok(())
    }
}

impl TryFrom<VirtualService> for balancerpb::VirtualService {
    type Error = String;

    fn try_from(vs: VirtualService) -> Result<Self, Self::Error> {
        let addr: IpAddr = vs.ip.parse().map_err(|e| format!("invalid IP: {}", e))?;

        // Parse allowed sources
        let allowed_srcs: Result<Vec<balancerpb::Subnet>, String> = vs
            .allowed_srcs
            .into_iter()
            .map(|s| parse_subnet(&s))
            .collect();
        let allowed_srcs = allowed_srcs?;

        // Parse peers
        let peers: Result<Vec<Vec<u8>>, String> = vs
            .peers
            .into_iter()
            .map(|p| {
                let addr: IpAddr = p.parse().map_err(|e| format!("invalid peer IP: {}", e))?;
                Ok(ip_to_bytes(addr))
            })
            .collect();
        let peers = peers?;

        Ok(Self {
            addr: ip_to_bytes(addr),
            port: vs.port as u32,
            proto: balancerpb::TransportProto::from(vs.proto) as i32,
            scheduler: balancerpb::VsScheduler::from(vs.scheduler) as i32,
            allowed_srcs,
            reals: vs.reals.into_iter().map(Into::into).collect(),
            flags: Some(vs.flags.into()),
            peers,
        })
    }
}

impl TryFrom<balancerpb::VirtualService> for VirtualService {
    type Error = String;

    fn try_from(vs: balancerpb::VirtualService) -> Result<Self, Self::Error> {
        let proto = balancerpb::TransportProto::try_from(vs.proto)
            .map_err(|_| "invalid proto")?
            .into();

        let scheduler = balancerpb::VsScheduler::try_from(vs.scheduler)
            .map_err(|_| "invalid scheduler")?
            .into();

        let allowed_srcs: Result<Vec<String>, String> = vs
            .allowed_srcs
            .into_iter()
            .map(|s| {
                let addr = bytes_to_ip(&s.addr)?;
                Ok(format!("{}/{}", addr, s.size))
            })
            .collect();

        let peers: Result<Vec<String>, String> = vs
            .peers
            .into_iter()
            .map(|p| Ok(bytes_to_ip(&p)?.to_string()))
            .collect();

        let reals: Result<Vec<Real>, String> = vs
            .reals
            .into_iter()
            .map(TryFrom::try_from)
            .collect();

        Ok(Self {
            ip: bytes_to_ip(&vs.addr)?.to_string(),
            proto,
            port: vs.port as u16,
            scheduler,
            flags: vs.flags.map(Into::into).unwrap_or_default(),
            allowed_srcs: allowed_srcs?,
            reals: reals?,
            peers: peers?,
        })
    }
}

////////////////////////////////////////////////////////////////////////////////
// Sessions Timeouts
////////////////////////////////////////////////////////////////////////////////

/// Timeouts for different session types.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionsTimeouts {
    pub tcp_syn_ack: u32,
    pub tcp_syn: u32,
    pub tcp_fin: u32,
    pub tcp: u32,
    pub udp: u32,
    pub default: u32,
}

impl Default for SessionsTimeouts {
    fn default() -> Self {
        Self {
            tcp_syn_ack: 10,
            tcp_syn: 10,
            tcp_fin: 10,
            tcp: 20,
            udp: 30,
            default: 60,
        }
    }
}

impl fmt::Display for SessionsTimeouts {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        writeln!(f, "Session Timeouts:")?;
        writeln!(f, "  TCP SYN-ACK: {}s", self.tcp_syn_ack)?;
        writeln!(f, "  TCP SYN: {}s", self.tcp_syn)?;
        writeln!(f, "  TCP FIN: {}s", self.tcp_fin)?;
        writeln!(f, "  TCP: {}s", self.tcp)?;
        writeln!(f, "  UDP: {}s", self.udp)?;
        writeln!(f, "  Default: {}s", self.default)?;
        Ok(())
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
// Balancer Addresses
////////////////////////////////////////////////////////////////////////////////

/// Balancer source and decap addresses.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BalancerAddresses {
    /// Source IPv4 address
    pub ipv4: String,

    /// Source IPv6 address
    pub ipv6: String,

    /// Decapsulation addresses
    #[serde(default)]
    pub decap: Vec<String>,
}

impl fmt::Display for BalancerAddresses {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        writeln!(f, "Balancer Addresses:")?;
        writeln!(f, "  Source IPv4: {}", self.ipv4)?;
        writeln!(f, "  Source IPv6: {}", self.ipv6)?;
        if !self.decap.is_empty() {
            writeln!(f, "  Decap Addresses:")?;
            for addr in &self.decap {
                writeln!(f, "    - {}", addr)?;
            }
        }
        Ok(())
    }
}

impl TryFrom<BalancerAddresses> for (Vec<u8>, Vec<u8>, Vec<Vec<u8>>) {
    type Error = String;

    fn try_from(addrs: BalancerAddresses) -> Result<Self, Self::Error> {
        let ipv4: Ipv4Addr = addrs
            .ipv4
            .parse()
            .map_err(|e| format!("invalid IPv4: {}", e))?;
        let ipv6: Ipv6Addr = addrs
            .ipv6
            .parse()
            .map_err(|e| format!("invalid IPv6: {}", e))?;

        let decap: Result<Vec<Vec<u8>>, String> = addrs
            .decap
            .into_iter()
            .map(|s| {
                let addr: IpAddr = s.parse().map_err(|e| format!("invalid decap IP: {}", e))?;
                Ok(ip_to_bytes(addr))
            })
            .collect();

        Ok((ipv4.octets().to_vec(), ipv6.octets().to_vec(), decap?))
    }
}

////////////////////////////////////////////////////////////////////////////////
// WLC Config
////////////////////////////////////////////////////////////////////////////////

/// Weighted Least Connections configuration.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WlcConfig {
    /// WLC power parameter
    #[serde(default = "default_wlc_power")]
    pub power: u64,

    /// Maximum real weight
    #[serde(default = "default_max_real_weight")]
    pub max_real_weight: u32,
}

fn default_wlc_power() -> u64 {
    10
}

fn default_max_real_weight() -> u32 {
    1000
}

impl Default for WlcConfig {
    fn default() -> Self {
        Self {
            power: default_wlc_power(),
            max_real_weight: default_max_real_weight(),
        }
    }
}

impl fmt::Display for WlcConfig {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "WLC Config: power={}, max_weight={}",
            self.power, self.max_real_weight
        )
    }
}

impl From<WlcConfig> for balancerpb::WlcConfig {
    fn from(config: WlcConfig) -> Self {
        Self {
            wlc_power: config.power,
            max_real_weight: config.max_real_weight,
        }
    }
}

impl From<balancerpb::WlcConfig> for WlcConfig {
    fn from(config: balancerpb::WlcConfig) -> Self {
        Self {
            power: config.wlc_power,
            max_real_weight: config.max_real_weight,
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// Module Config
////////////////////////////////////////////////////////////////////////////////

/// Complete balancer module configuration.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ModuleConfig {
    /// Virtual services
    pub virtual_services: Vec<VirtualService>,

    /// Source addresses
    pub addresses: BalancerAddresses,

    /// Session timeouts
    #[serde(default)]
    pub timeouts: SessionsTimeouts,

    /// WLC configuration
    #[serde(default)]
    pub wlc: WlcConfig,
}

impl fmt::Display for ModuleConfig {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        writeln!(f, "Balancer Module Configuration")?;
        writeln!(f, "==============================")?;
        writeln!(f)?;
        write!(f, "{}", self.addresses)?;
        writeln!(f)?;
        write!(f, "{}", self.timeouts)?;
        writeln!(f)?;
        writeln!(f, "{}", self.wlc)?;
        writeln!(f)?;
        writeln!(f, "Virtual Services ({}):", self.virtual_services.len())?;
        for (i, vs) in self.virtual_services.iter().enumerate() {
            writeln!(f, "\n[{}] {}", i, vs)?;
        }
        Ok(())
    }
}

impl TryFrom<ModuleConfig> for balancerpb::ModuleConfig {
    type Error = String;

    fn try_from(config: ModuleConfig) -> Result<Self, Self::Error> {
        let virtual_services: Result<Vec<balancerpb::VirtualService>, String> = config
            .virtual_services
            .into_iter()
            .map(TryInto::try_into)
            .collect();

        let (source_v4, source_v6, decap) = config.addresses.try_into()?;

        Ok(Self {
            virtual_services: virtual_services?,
            source_address_v4: source_v4,
            source_address_v6: source_v6,
            decap_addresses: decap,
            sessions_timeouts: Some(config.timeouts.into()),
            wlc: Some(config.wlc.into()),
        })
    }
}

impl TryFrom<balancerpb::ModuleConfig> for ModuleConfig {
    type Error = String;

    fn try_from(config: balancerpb::ModuleConfig) -> Result<Self, Self::Error> {
        let virtual_services: Result<Vec<VirtualService>, String> = config
            .virtual_services
            .into_iter()
            .map(TryFrom::try_from)
            .collect();

        let ipv4 = bytes_to_ip(&config.source_address_v4)?.to_string();
        let ipv6 = bytes_to_ip(&config.source_address_v6)?.to_string();

        let decap: Result<Vec<String>, String> = config
            .decap_addresses
            .into_iter()
            .map(|b| Ok(bytes_to_ip(&b)?.to_string()))
            .collect();

        Ok(Self {
            virtual_services: virtual_services?,
            addresses: BalancerAddresses {
                ipv4,
                ipv6,
                decap: decap?,
            },
            timeouts: config
                .sessions_timeouts
                .map(Into::into)
                .unwrap_or_default(),
            wlc: config.wlc.map(Into::into).unwrap_or_default(),
        })
    }
}

impl ModuleConfig {
    /// Load configuration from a YAML file.
    pub fn from_yaml_file(path: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let file = std::fs::File::open(path)?;
        let config = serde_yaml::from_reader(file)?;
        Ok(config)
    }

    /// Load configuration from a YAML string.
    pub fn from_yaml_str(yaml: &str) -> Result<Self, Box<dyn std::error::Error>> {
        let config = serde_yaml::from_str(yaml)?;
        Ok(config)
    }
}

////////////////////////////////////////////////////////////////////////////////
// Helper Functions
////////////////////////////////////////////////////////////////////////////////

fn ip_to_bytes(ip: IpAddr) -> Vec<u8> {
    match ip {
        IpAddr::V4(ipv4) => ipv4.octets().to_vec(),
        IpAddr::V6(ipv6) => ipv6.octets().to_vec(),
    }
}

fn bytes_to_ip(bytes: &[u8]) -> Result<IpAddr, String> {
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

fn parse_subnet(s: &str) -> Result<balancerpb::Subnet, String> {
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

////////////////////////////////////////////////////////////////////////////////
// Tests
////////////////////////////////////////////////////////////////////////////////

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_yaml_config() {
        let yaml = r#"
timeouts:
  tcp_syn_ack: 10
  tcp_syn: 10
  tcp_fin: 10
  tcp: 20
  udp: 30
  default: 60
virtual_services:
  - ip: "192.0.2.1"
    proto: tcp
    port: 5005
    flags:
      gre: false
      ops: false
      fix_mss: false
      pure_l3: false
    scheduler: wrr
    allowed_srcs:
      - "192.0.0.0/8"
    reals:
      - weight: 1
        dst: "4.5.6.7"
        src: "3.3.4.0"
        src_mask: "0.255.0.255"
        enabled: true
    peers:
      - "192.0.2.2"
      - "195.0.2.5"
addresses:
  ipv4: "191.11.13.15"
  ipv6: "2001:db8::1"
  decap:
    - "191.11.13.15"
    - "2001:db8::1"
"#;

        let config = ModuleConfig::from_yaml_str(yaml).unwrap();
        assert_eq!(config.virtual_services.len(), 1);
        assert_eq!(config.addresses.ipv4, "191.11.13.15");
        assert_eq!(config.addresses.ipv6, "2001:db8::1");
        assert_eq!(config.timeouts.tcp, 20);
    }

    #[test]
    fn test_proto_conversion() {
        let proto = Proto::Tcp;
        let pb: balancerpb::TransportProto = proto.into();
        let back: Proto = pb.into();
        assert_eq!(proto, back);
    }

    #[test]
    fn test_scheduler_conversion() {
        let sched = Scheduler::Wlc;
        let pb: balancerpb::VsScheduler = sched.into();
        let back: Scheduler = pb.into();
        assert_eq!(sched, back);
    }
}