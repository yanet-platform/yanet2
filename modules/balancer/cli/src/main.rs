mod config;
mod display;
mod service;

use std::error::Error;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use ync::logging;

use crate::service::BalancerService;

/// Balancer module CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ync::client::ConnectionArgs,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Update balancer configuration from YAML file.
    Update(UpdateCmd),
    /// List all balancer instances.
    List,
    /// Show balancer configuration.
    Config(ConfigCmd),
    /// Show balancer state (IPVS-style).
    Show(ShowCmd),
    /// Show active sessions (streaming).
    Sessions(SessionsCmd),
    /// Manage real servers.
    Reals(RealsCmd),
}

// ─── Update ──────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Balancer instance name.
    #[arg(long, short = 'n')]
    pub name: String,
    /// Path to YAML configuration file.
    #[arg(long, short = 'c')]
    pub config: String,
}

// ─── Config ──────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Parser)]
pub struct ConfigCmd {
    /// Balancer instance name (optional, auto-selects if only one exists).
    #[arg(long, short = 'n')]
    pub name: Option<String>,
}

// ─── Show ────────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Balancer instance name (optional, auto-selects if only one exists).
    #[arg(long, short = 'n')]
    pub name: Option<String>,

    /// Tabled output: VS info, scheduler, flags, reals with weights.
    #[arg(long, short = 't')]
    pub table: bool,

    /// Show all counters, active sessions and last packet timestamps.
    #[arg(long, short = 's')]
    pub stats: bool,

    /// Show allowed sources config per VS (with counters if --stats is
    /// present).
    #[arg(long, short = 'a')]
    pub acl: bool,

    /// Show peers per VS.
    #[arg(long)]
    pub peers: bool,

    /// Show decap addresses and source IPs.
    #[arg(long)]
    pub decap: bool,

    /// Enable all output sections (--table --stats --acl --peers --decap).
    #[arg(long, short = 'd')]
    pub detail: bool,

    #[command(flatten)]
    pub filter: FilterFlags,

    /// Filter by device name.
    #[arg(long)]
    pub device: Option<String>,
    /// Filter by pipeline name.
    #[arg(long, short = 'p')]
    pub pipeline: Option<String>,
    /// Filter by function name.
    #[arg(long, short = 'f')]
    pub function: Option<String>,
    /// Filter by chain name.
    #[arg(long)]
    pub chain: Option<String>,
}

impl ShowCmd {
    /// Whether counters should be requested from the server.
    pub fn include_counters(&self) -> bool {
        self.stats || self.detail
    }

    /// Whether tabled output mode is active.
    pub fn needs_table(&self) -> bool {
        self.table || self.stats || self.acl || self.peers || self.decap || self.detail
    }
}

// ─── Sessions ────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Parser)]
pub struct SessionsCmd {
    /// Balancer instance name (optional, auto-selects if only one exists).
    #[arg(long, short = 'n')]
    pub name: Option<String>,

    #[command(flatten)]
    pub filter: FilterFlags,
}

// ─── Reals ───────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Parser)]
pub struct RealsCmd {
    #[clap(subcommand)]
    pub mode: RealsMode,
}

#[derive(Debug, Clone, Parser)]
pub enum RealsMode {
    /// Enable real servers (buffered).
    Enable(EnableRealCmd),
    /// Disable real servers (buffered).
    Disable(DisableRealCmd),
    /// Flush buffered real server updates.
    Flush(FlushRealsCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct EnableRealCmd {
    /// Balancer instance name (optional, auto-selects if only one exists).
    #[arg(long, short = 'n')]
    pub name: Option<String>,
    /// Virtual service identifier: "ip:port/proto" or "[ipv6]:port/proto".
    #[arg(long)]
    pub vs: String,
    /// Real server IPs to enable.
    #[arg(long, required = true, num_args = 1..)]
    pub reals: Vec<String>,
    /// Optional new weight for the real servers.
    #[arg(long)]
    pub weight: Option<u32>,
    /// Flush buffered updates immediately after enabling.
    #[arg(long, default_value_t = false)]
    pub flush: bool,
}

#[derive(Debug, Clone, Parser)]
pub struct DisableRealCmd {
    /// Balancer instance name (optional, auto-selects if only one exists).
    #[arg(long, short = 'n')]
    pub name: Option<String>,
    /// Virtual service identifier: "ip:port/proto" or "[ipv6]:port/proto".
    #[arg(long)]
    pub vs: String,
    /// Real server IPs to disable.
    #[arg(long, required = true, num_args = 1..)]
    pub reals: Vec<String>,
    /// Flush buffered updates immediately after disabling.
    #[arg(long, default_value_t = false)]
    pub flush: bool,
}

#[derive(Debug, Clone, Parser)]
pub struct FlushRealsCmd {
    /// Balancer instance name (optional, auto-selects if only one exists).
    #[arg(long, short = 'n')]
    pub name: Option<String>,
}

// ─── Shared Filter Flags ─────────────────────────────────────────────────────

#[derive(Debug, Clone, Parser)]
pub struct FilterFlags {
    /// Filter by VIP address.
    #[arg(long)]
    pub vip: Option<String>,
    /// Filter by virtual service port.
    #[arg(long)]
    pub vs_port: Option<u32>,
    /// Filter by transport protocol (tcp or udp).
    #[arg(long)]
    pub proto: Option<Proto>,
    /// Filter by real server IP.
    #[arg(long)]
    pub real_ip: Option<String>,
    /// Filter by real server port.
    #[arg(long)]
    pub real_port: Option<u32>,
}

#[derive(Debug, Clone, clap::ValueEnum)]
pub enum Proto {
    Tcp,
    Udp,
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

use yanet_cli_balancer::balancerpb;

/// Parse a VS identifier string: "ip:port/proto", "[ipv6]:port/proto", or "ipv6:port/proto".
pub fn parse_vs_identifier(vs_str: &str) -> Result<(std::net::IpAddr, u16, balancerpb::TransportProto), String> {
    let vs_parts: Vec<&str> = vs_str.split('/').collect();
    if vs_parts.len() != 2 {
        return Err(format!(
            "invalid --vs format: '{}'. Expected: 'ip:port/proto', '[ipv6]:port/proto'",
            vs_str
        ));
    }

    let addr_port = vs_parts[0];
    let proto = match vs_parts[1].to_uppercase().as_str() {
        "TCP" => balancerpb::TransportProto::Tcp,
        "UDP" => balancerpb::TransportProto::Udp,
        other => return Err(format!("invalid proto: '{}'. Expected 'tcp' or 'udp'", other)),
    };

    let (ip_str, port_str) = if addr_port.starts_with('[') {
        let bracket_end = addr_port
            .find(']')
            .ok_or_else(|| format!("invalid IPv6 bracket notation: '{}'", addr_port))?;
        let ip_part = &addr_port[1..bracket_end];
        let remaining = &addr_port[bracket_end + 1..];
        if !remaining.starts_with(':') {
            return Err(format!("expected ':' after ']' in '{}'", addr_port));
        }
        (ip_part, &remaining[1..])
    } else {
        let parts: Vec<&str> = addr_port.rsplitn(2, ':').collect();
        if parts.len() != 2 {
            return Err(format!("invalid address:port format: '{}'", addr_port));
        }
        (parts[1], parts[0])
    };

    let port: u16 = port_str
        .parse()
        .map_err(|e| format!("invalid port '{}': {}", port_str, e))?;
    let ip: std::net::IpAddr = ip_str.parse().map_err(|e| format!("invalid IP '{}': {}", ip_str, e))?;

    Ok((ip, port, proto))
}

pub fn ip_to_bytes(ip: std::net::IpAddr) -> Vec<u8> {
    match ip {
        std::net::IpAddr::V4(v4) => v4.octets().to_vec(),
        std::net::IpAddr::V6(v6) => v6.octets().to_vec(),
    }
}

pub fn bytes_to_ip(bytes: &[u8]) -> Result<std::net::IpAddr, String> {
    match bytes.len() {
        4 => {
            let arr: [u8; 4] = bytes.try_into().map_err(|_| "invalid IPv4 bytes")?;
            Ok(std::net::IpAddr::V4(std::net::Ipv4Addr::from(arr)))
        }
        16 => {
            let arr: [u8; 16] = bytes.try_into().map_err(|_| "invalid IPv6 bytes")?;
            Ok(std::net::IpAddr::V6(std::net::Ipv6Addr::from(arr)))
        }
        n => Err(format!("invalid IP address length: {}", n)),
    }
}

pub fn format_ip_port(ip: std::net::IpAddr, port: u32) -> String {
    match ip {
        std::net::IpAddr::V4(_) => {
            if port == 0 {
                format!("{}", ip)
            } else {
                format!("{}:{}", ip, port)
            }
        }
        std::net::IpAddr::V6(_) => {
            if port == 0 {
                format!("{}", ip)
            } else {
                format!("[{}]:{}", ip, port)
            }
        }
    }
}

impl FilterFlags {
    pub fn to_proto(&self) -> Option<balancerpb::Filter> {
        if self.vip.is_none()
            && self.vs_port.is_none()
            && self.proto.is_none()
            && self.real_ip.is_none()
            && self.real_port.is_none()
        {
            return None;
        }

        Some(balancerpb::Filter {
            vip: self.vip.as_ref().map(|s| {
                let ip: std::net::IpAddr = s.parse().expect("invalid VIP address");
                ip_to_bytes(ip)
            }),
            vs_port: self.vs_port,
            proto: self.proto.as_ref().map(|p| match p {
                Proto::Tcp => balancerpb::TransportProto::Tcp as i32,
                Proto::Udp => balancerpb::TransportProto::Udp as i32,
            }),
            real_ip: self.real_ip.as_ref().map(|s| {
                let ip: std::net::IpAddr = s.parse().expect("invalid real IP address");
                ip_to_bytes(ip)
            }),
            real_port: self.real_port,
        })
    }
}

// ─── Entry Point ─────────────────────────────────────────────────────────────

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = BalancerService::connect(&cmd.connection).await?;
    service.handle(cmd.mode).await
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("failed to initialize logging");

    if let Err(err) = run(cmd).await {
        log::error!("{err}");
        std::process::exit(1);
    }
}
