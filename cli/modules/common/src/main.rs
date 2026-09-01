//! CLI for YANET "logging" core module.

use clap::{ArgAction, Parser, Subcommand, ValueEnum};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, Service},
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{logging_client::LoggingClient, UpdateLevelRequest};

const LOGGING_SERVICE: &str = "controlplane.ynpb.v1.Logging";

/// Common functionality.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
struct Cmd {
    #[command(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose in terms of logging.
    #[arg(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Subcommand)]
enum ModeCmd {
    /// Logging service.
    #[clap(subcommand)]
    Logging(LoggingCmd),
}

#[derive(Debug, Clone, Parser)]
enum LoggingCmd {
    /// Sets the new minimum log level.
    SetLevel(SetLogLevelCmd),
}

#[derive(Debug, Clone, Parser)]
struct SetLogLevelCmd {
    /// Minimum log level.
    level: LogLevel,
}

/// Log level for the logging service.
#[derive(Debug, Clone, ValueEnum)]
enum LogLevel {
    Debug,
    Info,
    Warn,
    Error,
}

impl From<LogLevel> for ynpb::pb::LogLevel {
    fn from(level: LogLevel) -> Self {
        match level {
            LogLevel::Debug => Self::Debug,
            LogLevel::Info => Self::Info,
            LogLevel::Warn => Self::Warn,
            LogLevel::Error => Self::Error,
        }
    }
}

pub fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

impl ModeCmd {
    pub fn action(&self) -> &'static str {
        match self {
            ModeCmd::Logging(LoggingCmd::SetLevel(..)) => "set-level",
        }
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = Service::connect_for(&cmd.connection, action, LOGGING_SERVICE, |channel| {
        LoggingClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip)
    })
    .await?;

    match cmd.mode {
        ModeCmd::Logging(LoggingCmd::SetLevel(cmd)) => {
            let request = UpdateLevelRequest {
                level: ynpb::pb::LogLevel::from(cmd.level.clone()).into(),
            };
            service
                .client()
                .update_level(request)
                .await
                .map_err(service.status(action))?;

            output::success(action, format_args!("Set log level to {:?}.", cmd.level));
        }
    }

    Ok(())
}
