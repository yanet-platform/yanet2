//! CLI command definitions

use clap::{ArgAction, Parser, ValueEnum};

use crate::{output, rpc::balancerpb};

////////////////////////////////////////////////////////////////////////////////
// Main Command
////////////////////////////////////////////////////////////////////////////////

/// Balancer module CLI
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: Mode,

    /// gRPC endpoint to send request
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,

    /// Log verbosity level
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbosity: u8,
}

////////////////////////////////////////////////////////////////////////////////
// Output Format
////////////////////////////////////////////////////////////////////////////////

/// Output format options
#[derive(Debug, Clone, Copy, ValueEnum)]
pub enum OutputFormat {
    /// JSON format
    Json,
    /// Tree structure
    Tree,
    /// Table format (default)
    Table,
}

impl From<OutputFormat> for crate::output::OutputFormat {
    fn from(format: OutputFormat) -> Self {
        match format {
            OutputFormat::Json => output::OutputFormat::Json,
            OutputFormat::Tree => output::OutputFormat::Tree,
            OutputFormat::Table => output::OutputFormat::Table,
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// Commands
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub enum Mode {
    /// Update balancer configuration from YAML file
    Update(UpdateCmd),
    /// Manage real servers
    Reals(RealsCmd),
    /// Show balancer configuration
    Config(ConfigCmd),
    /// List all balancer configurations
    List(ListCmd),
    /// Show configuration statistics
    Stats(StatsCmd),
    /// Show information about sessions
    Info(InfoCmd),
    /// Show active sessions
    Sessions(SessionsCmd),
    /// Show balancing graph with state and weights of reals
    Graph(GraphCmd),
}

////////////////////////////////////////////////////////////////////////////////
// Update Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Path to the YAML configuration file
    #[arg(long, short = 'c')]
    pub config: String,
}

////////////////////////////////////////////////////////////////////////////////
// Reals Commands
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct RealsCmd {
    #[clap(subcommand)]
    pub mode: RealsMode,
}

#[derive(Debug, Clone, Parser)]
pub enum RealsMode {
    /// Enable a real server (buffered)
    Enable(EnableRealCmd),
    /// Disable a real server (buffered)
    Disable(DisableRealCmd),
    /// Flush buffered real updates
    Flush(FlushRealUpdatesCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct EnableRealCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// IP of the virtual service
    #[arg(long)]
    pub virtual_ip: String,

    /// Protocol of the virtual service (tcp/udp)
    #[arg(long)]
    pub proto: String,

    /// Port of the virtual service
    #[arg(long)]
    pub virtual_port: u16,

    /// IP of the real server
    #[arg(long, short)]
    pub real_ip: String,

    /// Optional new weight for the real server
    #[arg(long)]
    pub weight: Option<u32>,
}

impl TryFrom<EnableRealCmd> for balancerpb::UpdateRealsRequest {
    type Error = String;

    fn try_from(cmd: EnableRealCmd) -> Result<Self, Self::Error> {
        let proto = match cmd.proto.to_uppercase().as_str() {
            "TCP" => balancerpb::TransportProto::Tcp,
            "UDP" => balancerpb::TransportProto::Udp,
            _ => return Err(format!("invalid proto: {}", cmd.proto)),
        };

        let virtual_ip: std::net::IpAddr = cmd
            .virtual_ip
            .parse()
            .map_err(|e| format!("invalid virtual IP: {}", e))?;
        let real_ip: std::net::IpAddr = cmd.real_ip.parse().map_err(|e| format!("invalid real IP: {}", e))?;

        let real_id = balancerpb::RealIdentifier {
            vs: Some(balancerpb::VsIdentifier {
                addr: Some(balancerpb::Addr {
                    bytes: match virtual_ip {
                        std::net::IpAddr::V4(ip) => ip.octets().to_vec(),
                        std::net::IpAddr::V6(ip) => ip.octets().to_vec(),
                    },
                }),
                port: cmd.virtual_port as u32,
                proto: proto as i32,
            }),
            real: Some(balancerpb::RelativeRealIdentifier {
                ip: Some(balancerpb::Addr {
                    bytes: match real_ip {
                        std::net::IpAddr::V4(ip) => ip.octets().to_vec(),
                        std::net::IpAddr::V6(ip) => ip.octets().to_vec(),
                    },
                }),
                port: 0,
            }),
        };

        Ok(Self {
            name: cmd.name,
            updates: vec![balancerpb::RealUpdate {
                real_id: Some(real_id),
                enable: Some(true),
                weight: cmd.weight,
            }],
            buffer: true, // Always buffer
        })
    }
}

#[derive(Debug, Clone, Parser)]
pub struct DisableRealCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// IP of the virtual service
    #[arg(long)]
    pub virtual_ip: String,

    /// Protocol of the virtual service (tcp/udp)
    #[arg(long)]
    pub proto: String,

    /// Port of the virtual service
    #[arg(long)]
    pub virtual_port: u16,

    /// IP of the real server
    #[arg(long, short)]
    pub real_ip: String,
}

impl TryFrom<DisableRealCmd> for balancerpb::UpdateRealsRequest {
    type Error = String;

    fn try_from(cmd: DisableRealCmd) -> Result<Self, Self::Error> {
        let proto = match cmd.proto.to_uppercase().as_str() {
            "TCP" => balancerpb::TransportProto::Tcp,
            "UDP" => balancerpb::TransportProto::Udp,
            _ => return Err(format!("invalid proto: {}", cmd.proto)),
        };

        let virtual_ip: std::net::IpAddr = cmd
            .virtual_ip
            .parse()
            .map_err(|e| format!("invalid virtual IP: {}", e))?;
        let real_ip: std::net::IpAddr = cmd.real_ip.parse().map_err(|e| format!("invalid real IP: {}", e))?;

        let real_id = balancerpb::RealIdentifier {
            vs: Some(balancerpb::VsIdentifier {
                addr: Some(balancerpb::Addr {
                    bytes: match virtual_ip {
                        std::net::IpAddr::V4(ip) => ip.octets().to_vec(),
                        std::net::IpAddr::V6(ip) => ip.octets().to_vec(),
                    },
                }),
                port: cmd.virtual_port as u32,
                proto: proto as i32,
            }),
            real: Some(balancerpb::RelativeRealIdentifier {
                ip: Some(balancerpb::Addr {
                    bytes: match real_ip {
                        std::net::IpAddr::V4(ip) => ip.octets().to_vec(),
                        std::net::IpAddr::V6(ip) => ip.octets().to_vec(),
                    },
                }),
                port: 0,
            }),
        };

        Ok(Self {
            name: cmd.name,
            updates: vec![balancerpb::RealUpdate {
                real_id: Some(real_id),
                enable: Some(false),
                weight: None,
            }],
            buffer: true, // Always buffer
        })
    }
}

#[derive(Debug, Clone, Parser)]
pub struct FlushRealUpdatesCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,
}

impl From<FlushRealUpdatesCmd> for balancerpb::FlushRealUpdatesRequest {
    fn from(cmd: FlushRealUpdatesCmd) -> Self {
        Self { name: cmd.name }
    }
}

////////////////////////////////////////////////////////////////////////////////
// Config Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct ConfigCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Output format
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

impl From<&ConfigCmd> for balancerpb::ShowConfigRequest {
    fn from(cmd: &ConfigCmd) -> Self {
        Self { name: cmd.name.clone() }
    }
}

////////////////////////////////////////////////////////////////////////////////
// List Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct ListCmd {
    /// Output format
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

////////////////////////////////////////////////////////////////////////////////
// Stats Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct StatsCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Device name (optional)
    #[arg(long)]
    pub device: Option<String>,

    /// Pipeline name (optional)
    #[arg(long)]
    pub pipeline: Option<String>,

    /// Function name (optional)
    #[arg(long)]
    pub function: Option<String>,

    /// Chain name (optional)
    #[arg(long)]
    pub chain: Option<String>,

    /// Output format
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

impl From<&StatsCmd> for balancerpb::ShowStatsRequest {
    fn from(cmd: &StatsCmd) -> Self {
        Self {
            name: cmd.name.clone(),
            r#ref: Some(balancerpb::PacketHandlerRef {
                device: cmd.device.clone(),
                pipeline: cmd.pipeline.clone(),
                function: cmd.function.clone(),
                chain: cmd.chain.clone(),
            }),
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// State Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct InfoCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Output format
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

impl From<&InfoCmd> for balancerpb::ShowInfoRequest {
    fn from(cmd: &InfoCmd) -> Self {
        Self { name: cmd.name.clone() }
    }
}

////////////////////////////////////////////////////////////////////////////////
// Sessions Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct SessionsCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Output format
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

impl From<&SessionsCmd> for balancerpb::ShowSessionsRequest {
    fn from(cmd: &SessionsCmd) -> Self {
        Self { name: cmd.name.clone() }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct GraphCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Output format
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

impl From<&GraphCmd> for balancerpb::ShowGraphRequest {
    fn from(cmd: &GraphCmd) -> Self {
        Self { name: cmd.name.clone() }
    }
}
