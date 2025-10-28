use clap::{ArgAction, Parser};

////////////////////////////////////////////////////////////////////////////////

/// CLI interface of the Balancer Module.
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

#[derive(Debug, Clone, Parser)]
pub enum Mode {
    Enable(EnableBalancingCmd),
    ShowConfig(ShowConfigCmd),
}