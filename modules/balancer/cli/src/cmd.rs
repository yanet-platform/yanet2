//! CLI command definitions

use clap::{ArgAction, Parser, ValueEnum};
use crate::rpc::{balancerpb, commonpb};

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
            OutputFormat::Json => crate::output::OutputFormat::Json,
            OutputFormat::Tree => crate::output::OutputFormat::Tree,
            OutputFormat::Table => crate::output::OutputFormat::Table,
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// Commands
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub enum Mode {
    /// Update balancer configuration from YAML file
    UpdateConfig(UpdateConfigCmd),
    /// Manage real servers
    Reals(RealsCmd),
    /// Show balancer configuration
    ShowConfig(ShowConfigCmd),
    /// List all balancer configurations
    ListConfigs(ListConfigsCmd),
    /// Show configuration statistics
    ConfigStats(ConfigStatsCmd),
    /// Show state information
    StateInfo(StateInfoCmd),
    /// Show active sessions information
    SessionsInfo(SessionsInfoCmd),
}

////////////////////////////////////////////////////////////////////////////////
// UpdateConfig Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct UpdateConfigCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Index of the dataplane instance
    #[arg(long, short, default_value_t = 0)]
    pub instance: u32,

    /// Path to the YAML configuration file
    #[arg(long, short = 'f')]
    pub config_file: String,
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
    /// Update a real server (always buffered)
    Update(UpdateRealCmd),
    /// Flush buffered real updates
    Flush(FlushRealUpdatesCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateRealCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Index of the dataplane instance
    #[arg(long, short, default_value_t = 0)]
    pub instance: u32,

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

    /// Enable the real server
    #[arg(long, conflicts_with = "disable")]
    pub enable: bool,

    /// Disable the real server
    #[arg(long, conflicts_with = "enable")]
    pub disable: bool,

    /// Optional new weight for the real server
    #[arg(long)]
    pub weight: Option<u32>,
}

impl TryFrom<UpdateRealCmd> for balancerpb::UpdateRealsRequest {
    type Error = String;

    fn try_from(cmd: UpdateRealCmd) -> Result<Self, Self::Error> {
        let proto = match cmd.proto.to_lowercase().as_str() {
            "tcp" => balancerpb::TransportProto::Tcp,
            "udp" => balancerpb::TransportProto::Udp,
            _ => return Err(format!("invalid proto: {}", cmd.proto)),
        };

        // If neither enable nor disable is specified, default to enable
        let enable = !cmd.disable;

        Ok(Self {
            target: Some(commonpb::TargetModule {
                config_name: cmd.name,
                dataplane_instance: cmd.instance,
            }),
            updates: vec![balancerpb::RealUpdate {
                virtual_ip: cmd.virtual_ip.into_bytes(),
                proto: proto as i32,
                port: cmd.virtual_port as u32,
                real_ip: cmd.real_ip.into_bytes(),
                enable,
                weight: cmd.weight.unwrap_or(0),
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

    /// Index of the dataplane instance
    #[arg(long, short, default_value_t = 0)]
    pub instance: u32,
}

impl From<FlushRealUpdatesCmd> for balancerpb::FlushRealUpdatesRequest {
    fn from(cmd: FlushRealUpdatesCmd) -> Self {
        Self {
            target: Some(commonpb::TargetModule {
                config_name: cmd.name,
                dataplane_instance: cmd.instance,
            }),
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// ShowConfig Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Index of the dataplane instance
    #[arg(long, short, default_value_t = 0)]
    pub instance: u32,

    /// Output format
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

impl From<&ShowConfigCmd> for balancerpb::ShowConfigRequest {
    fn from(cmd: &ShowConfigCmd) -> Self {
        Self {
            target: Some(commonpb::TargetModule {
                config_name: cmd.name.clone(),
                dataplane_instance: cmd.instance,
            }),
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// ListConfigs Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct ListConfigsCmd {
    /// Output format
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

////////////////////////////////////////////////////////////////////////////////
// ConfigStats Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct ConfigStatsCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Index of the dataplane instance
    #[arg(long, short, default_value_t = 0)]
    pub instance: u32,

    /// Device name
    #[arg(long)]
    pub device: String,

    /// Pipeline name
    #[arg(long)]
    pub pipeline: String,

    /// Function name
    #[arg(long)]
    pub function: String,

    /// Chain name
    #[arg(long)]
    pub chain: String,

    /// Output format
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

impl From<&ConfigStatsCmd> for balancerpb::ConfigStatsRequest {
    fn from(cmd: &ConfigStatsCmd) -> Self {
        Self {
            target: Some(commonpb::TargetModule {
                config_name: cmd.name.clone(),
                dataplane_instance: cmd.instance,
            }),
            dataplane_instance: cmd.instance,
            device: cmd.device.clone(),
            pipeline: cmd.pipeline.clone(),
            function: cmd.function.clone(),
            chain: cmd.chain.clone(),
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// StateInfo Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct StateInfoCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Index of the dataplane instance
    #[arg(long, short, default_value_t = 0)]
    pub instance: u32,

    /// Output format
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

impl From<&StateInfoCmd> for balancerpb::StateInfoRequest {
    fn from(cmd: &StateInfoCmd) -> Self {
        Self {
            target: Some(commonpb::TargetModule {
                config_name: cmd.name.clone(),
                dataplane_instance: cmd.instance,
            }),
        }
    }
}

////////////////////////////////////////////////////////////////////////////////
// SessionsInfo Command
////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct SessionsInfoCmd {
    /// Name of the module config
    #[arg(long, short = 'n')]
    pub name: String,

    /// Index of the dataplane instance
    #[arg(long, short, default_value_t = 0)]
    pub instance: u32,

    /// Output format
    #[clap(long, value_enum, default_value_t = OutputFormat::Table)]
    pub format: OutputFormat,
}

impl From<&SessionsInfoCmd> for balancerpb::SessionsInfoRequest {
    fn from(cmd: &SessionsInfoCmd) -> Self {
        Self {
            target: Some(commonpb::TargetModule {
                config_name: cmd.name.clone(),
                dataplane_instance: cmd.instance,
            }),
        }
    }
}