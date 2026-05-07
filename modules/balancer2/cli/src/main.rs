mod config;
mod display;
mod reals;
mod service;
mod sessions;

use std::{
    error::Error,
    fmt::{self, Display, Formatter},
    net::{AddrParseError, IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr},
    str::FromStr,
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
    pub vip: Option<IpAddr>,
    /// Filter by virtual service port.
    #[arg(long)]
    pub vs_port: Option<u16>,
    /// Filter by transport protocol (tcp or udp).
    #[arg(long)]
    pub proto: Option<Proto>,
    /// Filter by real server IP.
    #[arg(long)]
    pub real_ip: Option<IpAddr>,
    /// Filter by real server port.
    #[arg(long)]
    pub real_port: Option<u16>,
}

// Mirrors config::Proto for CLI filter flags; the two cannot share a type
// because of orphan-rule + derive constraints.
#[derive(Debug, Clone, clap::ValueEnum)]
pub enum Proto {
    Tcp,
    Udp,
}

#[derive(Debug, Clone)]
pub struct VsId {
    pub addr: IpAddr,
    pub port: u16,
    pub proto: balancerpb::TransportProto,
}

#[derive(Debug)]
pub enum VsIdParseError {
    MissingProto,
    InvalidSocket(AddrParseError),
    InvalidProto(String),
}

impl Display for VsIdParseError {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match self {
            Self::MissingProto => f.write_str("invalid --vs format: expected 'ip:port/proto' or '[ipv6]:port/proto'"),
            Self::InvalidSocket(e) => write!(f, "invalid address:port: {e}"),
            Self::InvalidProto(p) => write!(f, "invalid proto: '{p}'. Expected 'tcp' or 'udp'"),
        }
    }
}

impl Error for VsIdParseError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::InvalidSocket(e) => Some(e),
            _ => None,
        }
    }
}

impl FromStr for VsId {
    type Err = VsIdParseError;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let (addr_port, proto_str) = s.rsplit_once('/').ok_or(VsIdParseError::MissingProto)?;

        let proto = if proto_str.eq_ignore_ascii_case("tcp") {
            balancerpb::TransportProto::Tcp
        } else if proto_str.eq_ignore_ascii_case("udp") {
            balancerpb::TransportProto::Udp
        } else {
            return Err(VsIdParseError::InvalidProto(proto_str.to_string()));
        };

        let socket: SocketAddr = addr_port.parse().map_err(VsIdParseError::InvalidSocket)?;

        Ok(Self {
            addr: socket.ip(),
            port: socket.port(),
            proto,
        })
    }
}

impl Display for VsId {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        let proto = match self.proto {
            balancerpb::TransportProto::Tcp => "tcp",
            balancerpb::TransportProto::Udp => "udp",
        };
        write!(f, "{}/{}", format_ip_port(self.addr, self.port), proto)
    }
}

impl From<&VsId> for balancerpb::VsIdentifier {
    fn from(vs: &VsId) -> Self {
        Self {
            addr: ip_to_bytes(vs.addr),
            port: u32::from(vs.port),
            proto: vs.proto as i32,
        }
    }
}

pub fn ip_to_bytes(ip: IpAddr) -> Vec<u8> {
    match ip {
        IpAddr::V4(v4) => v4.octets().to_vec(),
        IpAddr::V6(v6) => v6.octets().to_vec(),
    }
}

#[derive(Debug)]
pub enum BytesToIpError {
    InvalidLength(usize),
}

impl Display for BytesToIpError {
    fn fmt(&self, f: &mut Formatter<'_>) -> Result<(), fmt::Error> {
        match self {
            Self::InvalidLength(n) => write!(f, "invalid IP address length: {n}"),
        }
    }
}

impl Error for BytesToIpError {}

pub fn bytes_to_ip(bytes: &[u8]) -> Result<IpAddr, BytesToIpError> {
    match bytes.len() {
        4 => {
            let arr = <[u8; 4]>::try_from(bytes).expect("length already checked");
            Ok(Ipv4Addr::from(arr).into())
        }
        16 => {
            let arr = <[u8; 16]>::try_from(bytes).expect("length already checked");
            Ok(Ipv6Addr::from(arr).into())
        }
        n => Err(BytesToIpError::InvalidLength(n)),
    }
}

/// Format an IP/port pair. Port 0 means a pure-L3 VS or "same port as VS"
/// for reals, so the port is omitted from the output.
pub fn format_ip_port(ip: IpAddr, port: u16) -> String {
    if port == 0 {
        ip.to_string()
    } else {
        SocketAddr::new(ip, port).to_string()
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
            vip: self.vip.map(ip_to_bytes),
            vs_port: self.vs_port.map(u32::from),
            proto: self.proto.as_ref().map(|p| match p {
                Proto::Tcp => balancerpb::TransportProto::Tcp as i32,
                Proto::Udp => balancerpb::TransportProto::Udp as i32,
            }),
            real_ip: self.real_ip.map(ip_to_bytes),
            real_port: self.real_port.map(u32::from),
        })
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
