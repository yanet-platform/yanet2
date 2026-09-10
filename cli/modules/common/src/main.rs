//! CLI for YANET "logging" core module.

use clap::{ArgAction, Parser, Subcommand, ValueEnum};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, Service},
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{GetLevelRequest, UpdateLevelRequest, logging_client::LoggingClient};

const LOGGING_SERVICE: &str = "controlplane.ynpb.v1.Logging";

/// Manages the log level of the control plane process (`yanet-controlplane`),
/// covering its Go logger and the hosted C libraries but not the dataplane.
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
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[arg(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Subcommand)]
enum ModeCmd {
    /// Manage the control plane's logging level.
    #[clap(subcommand)]
    Logging(LoggingCmd),
}

#[derive(Debug, Clone, Parser)]
enum LoggingCmd {
    /// Set the control plane's minimum log level.
    SetLevel(SetLogLevelCmd),
    /// Show the control plane's current minimum log level.
    Show,
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

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

impl ModeCmd {
    pub fn action(&self) -> &'static str {
        match self {
            ModeCmd::Logging(LoggingCmd::SetLevel(..)) => "set-level",
            ModeCmd::Logging(LoggingCmd::Show) => "show",
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
                .unary(action, request, async |client, request| {
                    client.update_level(request).await
                })
                .await?;

            let level_name = cmd.level.to_possible_value().expect("no skipped variants");
            output::success(
                action,
                format_args!("Set the control plane log level to '{}'.", level_name.get_name()),
            );
        }
        ModeCmd::Logging(LoggingCmd::Show) => {
            let response = service
                .unary(action, GetLevelRequest {}, async |client, request| {
                    client.get_level(request).await
                })
                .await?;

            output::data(
                || &response,
                || println!("level: {}", ynpb::log_level_name(response.level)),
            );
        }
    }

    Ok(())
}
