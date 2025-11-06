use clap::{ArgAction, Parser};

////////////////////////////////////////////////////////////////////////////////

/// Command line interface of the ACL Module.
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

/// Allows to enable ACL module with specified name.
#[derive(Debug, Clone, Parser)]
pub struct EnableAclCmd {
    /// Name of the module config.
    #[arg(long = "cfg", short = 'c')]
    pub config_name: String,

    /// Index of the dataplane instance.
    #[arg(long, short, required = false, default_value_t = 0)]
    pub instance: u32,

    /// Path to the file with rules description.
    #[arg(long = "rules", short, required = true)]
    pub rules_path: String,
}

#[derive(Debug, Clone, Parser)]
pub enum Mode {
    Enable(EnableAclCmd),
}
