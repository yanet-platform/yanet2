mod config;
mod display;
mod reals;
mod service;
mod sessions;

use std::{
    error::Error,
    net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr},
};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use yanet_cli_balancer2::balancerpb;
use ync::logging;

use crate::service::Balancer2Service;

/// Balancer2 module CLI.
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
    /// Create or update a balancer configuration from a YAML file.
    Update(UpdateCmd),
    /// List all balancer configurations.
    List,
    /// Show a balancer configuration.
    Config(ConfigCmd),
    /// Show balancer state (IPVS-style).
    Show(ShowCmd),
    /// Manage sessions states.
    Sessions(sessions::SessionsCmd),
    /// Show balancer metrics (JSON).
    Metrics(MetricsCmd),
    /// Manage real servers.
    Reals(reals::RealsCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Balancer configuration name.
    #[arg(long, short = 'n')]
    pub name: String,
    /// Path to YAML configuration file.
    #[arg(long, short = 'c')]
    pub config: String,
    /// Sessions state name to bind this configuration to (required on first
    /// create; optional on subsequent updates of an existing config).
    #[arg(long, short = 's')]
    pub sessions: Option<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct ConfigCmd {
    /// Balancer configuration name.
    #[arg(long, short = 'n')]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Balancer configuration name.
    #[arg(long, short = 'n')]
    pub name: String,

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

    /// Enable all output sections (--stats --acl --peers --decap).
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

#[derive(Debug, Clone, Parser)]
pub struct MetricsCmd {}

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

// Mirrors config::Proto for CLI filter flags; the two cannot share a type
// because of orphan-rule + derive constraints.
#[derive(Debug, Clone, clap::ValueEnum)]
pub enum Proto {
    Tcp,
    Udp,
}

/// Parse a VS identifier string: "ip:port/proto" or "[ipv6]:port/proto".
pub fn parse_vs_identifier(vs: &str) -> Result<(IpAddr, u16, balancerpb::TransportProto), Box<dyn Error>> {
    let vs_parts: Vec<&str> = vs.split('/').collect();
    if vs_parts.len() != 2 {
        return Err(format!(
            "invalid --vs format: '{}'. Expected: 'ip:port/proto' or '[ipv6]:port/proto'",
            vs
        )
        .into());
    }

    let addr_port = vs_parts[0];
    let proto = if vs_parts[1].eq_ignore_ascii_case("tcp") {
        balancerpb::TransportProto::Tcp
    } else if vs_parts[1].eq_ignore_ascii_case("udp") {
        balancerpb::TransportProto::Udp
    } else {
        return Err(format!("invalid proto: '{}'. Expected 'tcp' or 'udp'", vs_parts[1]).into());
    };

    let socket: SocketAddr = addr_port
        .parse()
        .map_err(|e| format!("invalid address:port '{}': {}", addr_port, e))?;

    Ok((socket.ip(), socket.port(), proto))
}

pub fn ip_to_bytes(ip: IpAddr) -> Vec<u8> {
    match ip {
        IpAddr::V4(v4) => v4.octets().to_vec(),
        IpAddr::V6(v6) => v6.octets().to_vec(),
    }
}

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
        n => Err(format!("invalid IP address length: {}", n)),
    }
}

pub fn format_ip_port(ip: IpAddr, port: u32) -> String {
    match ip {
        IpAddr::V4(_) => {
            if port == 0 {
                format!("{}", ip)
            } else {
                format!("{}:{}", ip, port)
            }
        }
        IpAddr::V6(_) => {
            if port == 0 {
                format!("{}", ip)
            } else {
                format!("[{}]:{}", ip, port)
            }
        }
    }
}

impl FilterFlags {
    pub fn to_proto(&self) -> Result<Option<balancerpb::Filter>, Box<dyn Error>> {
        if self.vip.is_none()
            && self.vs_port.is_none()
            && self.proto.is_none()
            && self.real_ip.is_none()
            && self.real_port.is_none()
        {
            return Ok(None);
        }

        let vip = match &self.vip {
            Some(s) => {
                let ip: IpAddr = s.parse().map_err(|e| format!("invalid VIP '{}': {}", s, e))?;
                Some(ip_to_bytes(ip))
            }
            None => None,
        };
        let real_ip = match &self.real_ip {
            Some(s) => {
                let ip: IpAddr = s.parse().map_err(|e| format!("invalid real IP '{}': {}", s, e))?;
                Some(ip_to_bytes(ip))
            }
            None => None,
        };

        Ok(Some(balancerpb::Filter {
            vip,
            vs_port: self.vs_port,
            proto: self.proto.as_ref().map(|p| match p {
                Proto::Tcp => balancerpb::TransportProto::Tcp as i32,
                Proto::Udp => balancerpb::TransportProto::Udp as i32,
            }),
            real_ip,
            real_port: self.real_port,
        }))
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = Balancer2Service::connect(&cmd.connection).await?;
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
