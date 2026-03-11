//! CLI for YANET "logging" core module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, Subcommand, ValueEnum};
use clap_complete::CompleteEnv;
use tonic::codec::CompressionEncoding;
use ync::{client::ConnectionArgs, logging};
use ynpb::pb::{logging_client::LoggingClient, UpdateLevelRequest};

/// Common functionality.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
struct Cmd {
    #[command(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
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
    logging::init(cmd.verbose as usize).expect("no error expected");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let channel = ync::client::connect(&cmd.connection).await?;
    let mut client = LoggingClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip);

    match cmd.mode {
        ModeCmd::Logging(LoggingCmd::SetLevel(cmd)) => {
            let request = UpdateLevelRequest {
                level: ynpb::pb::LogLevel::from(cmd.level).into(),
            };
            let _ = client.update_level(request).await?;
            println!("OK");
        }
    }

    Ok(())
}
