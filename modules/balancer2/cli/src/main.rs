mod config;
mod display;
mod reals;
mod service;
mod sessions;

use core::{
    fmt::{self, Display, Formatter},
    net::{AddrParseError, IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr},
    str::FromStr,
};
use std::path::PathBuf;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use tonic::codec::CompressionEncoding;
use yanet_cli_balancer2::balancerpb;
use ync::{client::ConnectionArgs, completion, errors::Error, output::CommonFormat};

use crate::service::Balancer2Service;

/// Balancer2 module CLI.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
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

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::Update(..) => "update",
            Self::List => "list",
            Self::Config(..) => "config",
            Self::Show(..) => "show",
            Self::Sessions(cmd) => match &cmd.mode {
                sessions::SessionsMode::List => "sessions list",
                sessions::SessionsMode::Show(..) => "sessions show",
                sessions::SessionsMode::Update(..) => "sessions update",
            },
            Self::Metrics(..) => "metrics",
            Self::Reals(cmd) => match &cmd.mode {
                reals::RealsMode::Enable(..) => "enable",
                reals::RealsMode::Disable(..) => "disable",
            },
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Balancer configuration name.
    #[arg(long, short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub name: String,
    /// Path to the YAML configuration file.
    #[arg(value_name = "PATH")]
    pub file: PathBuf,
    /// Sessions state name to bind this configuration to (required on first
    /// create; optional on subsequent updates of an existing config).
    #[arg(long, short_alias = 's', add = ArgValueCandidates::new(sessions_candidates))]
    pub sessions: Option<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct ConfigCmd {
    /// Balancer configuration name.
    #[arg(long, short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Balancer configuration name.
    #[arg(long, short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub name: String,

    /// Show all counters, active sessions and last packet timestamps.
    #[arg(long, short_alias = 's')]
    pub stats: bool,

    /// Show allowed sources config per VS (with counters if --stats is
    /// present).
    #[arg(long, short_alias = 'a')]
    pub acl: bool,

    /// Show peers per VS.
    #[arg(long)]
    pub peers: bool,

    /// Show decap addresses and source IPs.
    #[arg(long)]
    pub decap: bool,

    /// Enable all output sections (--stats --acl --peers --decap).
    #[arg(long)]
    pub detail: bool,

    #[command(flatten)]
    pub filter: FilterFlags,

    /// Filter by device name.
    #[arg(long, short = 'd')]
    pub device: Option<String>,
    /// Filter by pipeline name.
    #[arg(long, short = 'p')]
    pub pipeline: Option<String>,
    /// Filter by function name.
    #[arg(long, short = 'f')]
    pub function: Option<String>,
    /// Filter by chain name.
    #[arg(long, short = 'c')]
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

impl core::error::Error for VsIdParseError {
    fn source(&self) -> Option<&(dyn core::error::Error + 'static)> {
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

impl core::error::Error for BytesToIpError {}

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

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = Balancer2Service::connect(&cmd.connection, action).await?;
    service.handle(cmd.mode, cmd.format).await
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

/// Completion candidates for a `--name` argument: the balancer configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            balancerpb::balancer_client::BalancerClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| {
            Ok(client
                .list_configs(balancerpb::ListConfigsRequest {})
                .await?
                .into_inner()
                .names)
        },
    )
}

/// Completion candidates for a sessions-state name argument: the sessions
/// states the module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn sessions_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            balancerpb::balancer_client::BalancerClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| {
            Ok(client
                .list_sessions_states(balancerpb::ListSessionsStatesRequest {})
                .await?
                .into_inner()
                .names)
        },
    )
}
