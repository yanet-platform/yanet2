use clap::{ArgAction, Parser};

use crate::rpc::{balancerpb, commonpb};

////////////////////////////////////////////////////////////////////////////////

/// Command line interface of the Balancer Module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: Mode,

    /// GRPC endpoint to send request.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,

    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbosity: u8,
}

/// Allows to enable balancer module with specified name.
#[derive(Debug, Clone, Parser)]
pub struct EnableBalancingCmd {
    /// Name of the module config.
    #[arg(long = "cfg", short = 'c')]
    pub config_name: String,

    /// Index of the dataplane instance.
    #[arg(long, short, required = false, default_value_t = 0)]
    pub instance: u32,

    /// Path to the file with virtual services configuration.
    #[arg(long = "services", short, required = true)]
    pub services_path: String,

    /// Number of sessions to reserve in the sessions table.
    #[arg(long = "reserve", required = false, default_value_t = 256)]
    pub sessions_table_reserve: u64,
}

/// Allows to show module config with specified name.
#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Name of the module config.
    #[arg(long = "cfg", short = 'c')]
    pub config_name: String,

    /// Index of the dataplane instance.
    #[arg(long, short, required = false, default_value_t = 0)]
    pub instance: u32,
}

/// Allows to schedule enable of the real server.
#[derive(Debug, Clone, Parser)]
pub struct EnableRealCmd {
    /// Name of the module config.
    #[arg(long = "cfg", short = 'c')]
    pub config_name: String,

    /// Index of the dataplane instance.
    #[arg(long, short, required = false, default_value_t = 0)]
    pub instance: u32,

    /// Ip of the virtual service.
    #[arg(long, required = true)]
    pub virtual_ip: String,

    /// Proto of the virtual service
    #[arg(long, required = true)]
    pub proto: String,

    /// Port of the virtual service
    #[arg(long, required = true)]
    pub virtual_port: u16,

    /// Ip of the real server
    #[arg(long, short, required = true)]
    pub real_ip: String,

    /// Port of the real server
    #[arg(long, required = false, default_value_t = 0)]
    #[allow(unused)]
    pub real_port: u16,

    #[arg(long, required = false, default_value = None)]
    pub real_weight: Option<u16>,
}

impl TryFrom<EnableRealCmd> for balancerpb::UpdateRealsRequest {
    type Error = String;
    fn try_from(cmd: EnableRealCmd) -> Result<Self, Self::Error> {
        let proto = cmd.proto.to_lowercase();
        let proto = match proto.as_str() {
            "tcp" => balancerpb::TransportProto::Tcp,
            "udp" => balancerpb::TransportProto::Udp,
            _ => return Err(format!("unexpected proto: {}", cmd.proto)),
        };

        let result = Self {
            target: Some(commonpb::TargetModule {
                config_name: cmd.config_name,
                dataplane_instance: cmd.instance,
            }),
            updates: vec![balancerpb::RealUpdate {
                virtual_ip: cmd.virtual_ip.into(),
                proto: proto as i32,
                port: cmd.virtual_port as u32,
                real_ip: cmd.real_ip.into(),
                weight: cmd.real_weight.unwrap_or(0) as u32,
                enable: true,
            }],
            buffer: true,
        };

        Ok(result)
    }
}

/// Allows to schedule enable of the real server.
#[derive(Debug, Clone, Parser)]
pub struct DisableRealCmd {
    /// Name of the module config.
    #[arg(long = "cfg", short = 'c')]
    pub config_name: String,

    /// Index of the dataplane instance.
    #[arg(long, short, required = false, default_value_t = 0)]
    pub instance: u32,

    /// Ip of the virtual service.
    #[arg(long, required = true)]
    pub virtual_ip: String,

    /// Proto of the virtual service
    #[arg(long, required = true)]
    pub proto: String,

    /// Port of the virtual service
    #[arg(long, required = true)]
    pub virtual_port: u16,

    /// Ip of the real server
    #[arg(long, short, required = true)]
    pub real_ip: String,

    /// Port of the real server
    #[arg(long, required = false, default_value_t = 0)]
    #[allow(unused)]
    pub real_port: u16,

    #[arg(long, required = false, default_value = None)]
    pub real_weight: Option<u16>,
}

impl TryFrom<DisableRealCmd> for balancerpb::UpdateRealsRequest {
    type Error = String;
    fn try_from(cmd: DisableRealCmd) -> Result<Self, Self::Error> {
        let proto = cmd.proto.to_lowercase();
        let proto = match proto.as_str() {
            "tcp" => balancerpb::TransportProto::Tcp,
            "udp" => balancerpb::TransportProto::Udp,
            _ => return Err(format!("unexpected proto: {}", cmd.proto)),
        };

        Ok(Self {
            target: Some(commonpb::TargetModule {
                config_name: cmd.config_name,
                dataplane_instance: cmd.instance,
            }),
            updates: vec![balancerpb::RealUpdate {
                virtual_ip: cmd.virtual_ip.into(),
                proto: proto as i32,
                port: cmd.virtual_port as u32,
                real_ip: cmd.real_ip.into(),
                weight: cmd.real_weight.unwrap_or(0) as u32,
                enable: false,
            }],
            buffer: true,
        })
    }
}

/// Allows to flush scheduled updates of the real servers.
#[derive(Debug, Clone, Parser)]
pub struct FlushRealUpdatesCmd {
    /// Name of the module config.
    #[arg(long = "cfg", short = 'c')]
    pub config_name: String,

    /// Index of the dataplane instance.
    #[arg(long, short, required = false, default_value_t = 0)]
    pub instance: u32,
}

impl From<FlushRealUpdatesCmd> for balancerpb::FlushRealUpdatesRequest {
    fn from(cmd: FlushRealUpdatesCmd) -> Self {
        Self {
            target: Some(commonpb::TargetModule {
                config_name: cmd.config_name,
                dataplane_instance: cmd.instance,
            }),
        }
    }
}

#[derive(Debug, Clone, Parser)]
#[command(flatten_help = true)]
pub enum RealMode {
    Enable(EnableRealCmd),
    Disable(DisableRealCmd),
    Flush(FlushRealUpdatesCmd),
}

/// Allows to enable and disable reals.
#[derive(Debug, Clone, Parser)]
pub struct RealCmds {
    #[clap(subcommand)]
    pub mode: RealMode,
}

////////////////////////////////////////////////////////////////////////////////

#[derive(Debug, Clone, Parser)]
pub struct StateInfoCmd {
    /// Name of the module config.
    #[arg(long = "cfg", short = 'c')]
    pub config_name: String,

    /// Index of the dataplane instance.
    #[arg(long, short, required = false, default_value_t = 0)]
    pub instance: u32,
}

#[derive(Debug, Clone, Parser)]
pub struct ConfigInfoCmd {
    /// Index of the dataplane instance.
    #[arg(long, short, required = false, default_value_t = 0)]
    pub instance: u32,

    #[arg(long = "cfg", short = 'c')]
    pub config_name: String,

    #[arg(long)]
    pub device: Option<String>,

    #[arg(long)]
    pub pipeline: Option<String>,

    #[arg(long)]
    pub function: Option<String>,

    #[arg(long)]
    pub chain: Option<String>,
}

#[derive(Debug, Clone, Parser)]
#[command(flatten_help = true)]
pub enum InfoMode {
    State(StateInfoCmd),
    Config(ConfigInfoCmd),
}

/// Allows to print statistics about balancer.
#[derive(Debug, Clone, Parser)]
pub struct InfoCmds {
    #[clap(subcommand)]
    pub mode: InfoMode,
}

#[derive(Debug, Clone, Parser)]
pub enum Mode {
    Enable(EnableBalancingCmd),
    ShowConfig(ShowConfigCmd),
    Real(RealCmds),
    Info(InfoCmds),
}
