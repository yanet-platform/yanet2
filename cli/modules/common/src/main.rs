//! CLI for YANET "logging" core module.

use clap::{ArgAction, CommandFactory, Parser, Subcommand, ValueEnum};
use clap_complete::CompleteEnv;
use tonic::codec::CompressionEncoding;
use ync::{
    client::ConnectionArgs,
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{logging_client::LoggingClient, UpdateLevelRequest};

const LOGGING_SERVICE: &str = "ynpb.Logging";

/// Common functionality.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
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

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
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
    let endpoint = cmd.connection.endpoint.clone();

    let channel = ync::client::connect(&cmd.connection)
        .await
        .map_err(|err| Error::from_connection(err, action, endpoint.clone()))?;

    let mut client = LoggingClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip);

    match cmd.mode {
        ModeCmd::Logging(LoggingCmd::SetLevel(cmd)) => {
            let request = UpdateLevelRequest {
                level: ynpb::pb::LogLevel::from(cmd.level.clone()).into(),
            };
            client
                .update_level(request)
                .await
                .map_err(|status| Error::from_status(status, action, endpoint.clone(), LOGGING_SERVICE))?;

            output::success(action, format_args!("set log level to {:?}", cmd.level));
        }
    }

    Ok(())
}
