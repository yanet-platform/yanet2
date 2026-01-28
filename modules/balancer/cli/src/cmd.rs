//! CLI command definitions

use clap::{ArgAction, Parser};

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

/// Helper struct for output format flags
#[derive(Debug, Clone, Parser)]
pub struct FormatFlags {
    /// Output in JSON format
    #[clap(long, short = 'j', conflicts_with_all = ["tree", "table"])]
    pub json: bool,

    /// Output in tree format
    #[clap(long, short = 't', conflicts_with_all = ["json", "table"])]
    pub tree: bool,

    /// Output in table format (default)
    #[clap(long, conflicts_with_all = ["json", "tree"])]
    pub table: bool,
}

impl FormatFlags {
    /// Convert flags to OutputFormat, defaulting to Table if none specified
    pub fn to_format(&self) -> crate::output::OutputFormat {
        if self.json {
            output::OutputFormat::Json
        } else if self.tree {
            output::OutputFormat::Tree
        } else {
            // Default to table if no format specified or if --table is explicitly set
            output::OutputFormat::Table
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
    /// Name of the module config (optional, auto-selects if only one exists)
    #[arg(long, short = 'n')]
    pub name: Option<String>,

    /// Virtual service in format "ip:port/proto" (e.g., "192.168.1.1:80/tcp")
    #[arg(long)]
    pub vs: String,

    /// List of real server IPs to enable
    #[arg(long, required = true, num_args = 1..)]
    pub reals: Vec<String>,

    /// Optional new weight for the real servers
    #[arg(long)]
    pub weight: Option<u32>,

    /// Flush buffered updates immediately after enabling
    #[arg(long, default_value_t = false)]
    pub flush: bool,
}

impl TryFrom<EnableRealCmd> for balancerpb::UpdateRealsRequest {
    type Error = String;

    fn try_from(cmd: EnableRealCmd) -> Result<Self, Self::Error> {
        // Parse the --vs option in format "ip:port/proto"
        let vs_parts: Vec<&str> = cmd.vs.split('/').collect();
        if vs_parts.len() != 2 {
            return Err(format!(
                "invalid --vs format: '{}'. Expected format: 'ip:port/proto' (e.g., '192.168.1.1:80/tcp')",
                cmd.vs
            ));
        }

        let addr_port = vs_parts[0];
        let proto_str = vs_parts[1];

        // Parse protocol (case-insensitive)
        let proto = match proto_str.to_uppercase().as_str() {
            "TCP" => balancerpb::TransportProto::Tcp,
            "UDP" => balancerpb::TransportProto::Udp,
            _ => {
                return Err(format!(
                    "invalid proto: '{}'. Expected 'tcp' or 'udp' (case-insensitive)",
                    proto_str
                ));
            }
        };

        // Parse IP and port
        let addr_port_parts: Vec<&str> = addr_port.rsplitn(2, ':').collect();
        if addr_port_parts.len() != 2 {
            return Err(format!(
                "invalid address:port format: '{}'. Expected format: 'ip:port'",
                addr_port
            ));
        }

        let port_str = addr_port_parts[0];
        let ip_str = addr_port_parts[1];

        let virtual_port: u16 = port_str
            .parse()
            .map_err(|e| format!("invalid port '{}': {}", port_str, e))?;

        let virtual_ip: std::net::IpAddr = ip_str
            .parse()
            .map_err(|e| format!("invalid virtual IP '{}': {}", ip_str, e))?;

        // Create updates for all real IPs
        let mut updates = Vec::new();
        for real_ip_str in &cmd.reals {
            let real_ip: std::net::IpAddr = real_ip_str
                .parse()
                .map_err(|e| format!("invalid real IP '{}': {}", real_ip_str, e))?;

            let real_id = balancerpb::RealIdentifier {
                vs: Some(balancerpb::VsIdentifier {
                    addr: Some(balancerpb::Addr {
                        bytes: match virtual_ip {
                            std::net::IpAddr::V4(ip) => ip.octets().to_vec(),
                            std::net::IpAddr::V6(ip) => ip.octets().to_vec(),
                        },
                    }),
                    port: virtual_port as u32,
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

            updates.push(balancerpb::RealUpdate {
                real_id: Some(real_id),
                enable: Some(true),
                weight: cmd.weight,
            });
        }

        Ok(Self {
            name: cmd.name,
            updates,
            buffer: true, // Always buffer
        })
    }
}

#[derive(Debug, Clone, Parser)]
pub struct DisableRealCmd {
    /// Name of the module config (optional, auto-selects if only one exists)
    #[arg(long, short = 'n')]
    pub name: Option<String>,

    /// Virtual service in format "ip:port/proto" (e.g., "192.168.1.1:80/tcp")
    #[arg(long)]
    pub vs: String,

    /// List of real server IPs to disable
    #[arg(long, required = true, num_args = 1..)]
    pub reals: Vec<String>,

    /// Flush buffered updates immediately after disabling
    #[arg(long, default_value_t = false)]
    pub flush: bool,
}

impl TryFrom<DisableRealCmd> for balancerpb::UpdateRealsRequest {
    type Error = String;

    fn try_from(cmd: DisableRealCmd) -> Result<Self, Self::Error> {
        // Parse the --vs option in format "ip:port/proto"
        let vs_parts: Vec<&str> = cmd.vs.split('/').collect();
        if vs_parts.len() != 2 {
            return Err(format!(
                "invalid --vs format: '{}'. Expected format: 'ip:port/proto' (e.g., '192.168.1.1:80/tcp')",
                cmd.vs
            ));
        }

        let addr_port = vs_parts[0];
        let proto_str = vs_parts[1];

        // Parse protocol (case-insensitive)
        let proto = match proto_str.to_uppercase().as_str() {
            "TCP" => balancerpb::TransportProto::Tcp,
            "UDP" => balancerpb::TransportProto::Udp,
            _ => {
                return Err(format!(
                    "invalid proto: '{}'. Expected 'tcp' or 'udp' (case-insensitive)",
                    proto_str
                ));
            }
        };

        // Parse IP and port
        let addr_port_parts: Vec<&str> = addr_port.rsplitn(2, ':').collect();
        if addr_port_parts.len() != 2 {
            return Err(format!(
                "invalid address:port format: '{}'. Expected format: 'ip:port'",
                addr_port
            ));
        }

        let port_str = addr_port_parts[0];
        let ip_str = addr_port_parts[1];

        let virtual_port: u16 = port_str
            .parse()
            .map_err(|e| format!("invalid port '{}': {}", port_str, e))?;

        let virtual_ip: std::net::IpAddr = ip_str
            .parse()
            .map_err(|e| format!("invalid virtual IP '{}': {}", ip_str, e))?;

        // Create updates for all real IPs
        let mut updates = Vec::new();
        for real_ip_str in &cmd.reals {
            let real_ip: std::net::IpAddr = real_ip_str
                .parse()
                .map_err(|e| format!("invalid real IP '{}': {}", real_ip_str, e))?;

            let real_id = balancerpb::RealIdentifier {
                vs: Some(balancerpb::VsIdentifier {
                    addr: Some(balancerpb::Addr {
                        bytes: match virtual_ip {
                            std::net::IpAddr::V4(ip) => ip.octets().to_vec(),
                            std::net::IpAddr::V6(ip) => ip.octets().to_vec(),
                        },
                    }),
                    port: virtual_port as u32,
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

            updates.push(balancerpb::RealUpdate {
                real_id: Some(real_id),
                enable: Some(false),
                weight: None,
            });
        }

        Ok(Self {
            name: cmd.name,
            updates,
            buffer: true, // Always buffer
        })
    }
}

#[derive(Debug, Clone, Parser)]
pub struct FlushRealUpdatesCmd {
    /// Name of the module config (optional, auto-selects if only one exists)
    #[arg(long, short = 'n')]
    pub name: Option<String>,
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
    /// Name of the module config (optional, auto-selects if only one exists)
    #[arg(long, short = 'n')]
    pub name: Option<String>,

    #[clap(flatten)]
    pub format: FormatFlags,
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
    #[clap(flatten)]
    pub format: FormatFlags,
}

////////////////////////////////////////////////////////////////////////////////
// Stats Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct StatsCmd {
    /// Name of the module config (optional, auto-selects if only one exists)
    #[arg(long, short = 'n')]
    pub name: Option<String>,

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

    #[clap(flatten)]
    pub format: FormatFlags,
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
    /// Name of the module config (optional, auto-selects if only one exists)
    #[arg(long, short = 'n')]
    pub name: Option<String>,

    #[clap(flatten)]
    pub format: FormatFlags,
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
    /// Name of the module config (optional, auto-selects if only one exists)
    #[arg(long, short = 'n')]
    pub name: Option<String>,

    #[clap(flatten)]
    pub format: FormatFlags,
}

impl From<&SessionsCmd> for balancerpb::ShowSessionsRequest {
    fn from(cmd: &SessionsCmd) -> Self {
        Self { name: cmd.name.clone() }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct GraphCmd {
    /// Name of the module config (optional, auto-selects if only one exists)
    #[arg(long, short = 'n')]
    pub name: Option<String>,

    #[clap(flatten)]
    pub format: FormatFlags,
}

impl From<&GraphCmd> for balancerpb::ShowGraphRequest {
    fn from(cmd: &GraphCmd) -> Self {
        Self { name: cmd.name.clone() }
    }
}
