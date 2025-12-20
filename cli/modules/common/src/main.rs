//! CLI for YANET "logging" core module.

use core::error::Error;

use clap::{builder::PossibleValue, ArgAction, CommandFactory, Parser, Subcommand, ValueEnum};
use clap_complete::CompleteEnv;
use tonic::{codec::CompressionEncoding, transport::Channel};
use ync::logging;
use ynpb::{logging_client::LoggingClient, UpdateLevelRequest};

#[allow(non_snake_case)]
pub mod ynpb {
    tonic::include_proto!("ynpb");
}

/// Common functionality.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
struct Cmd {
    #[command(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[arg(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
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
    level: ynpb::LogLevel,
}

impl ValueEnum for ynpb::LogLevel {
    fn value_variants<'a>() -> &'a [Self] {
        &[Self::Debug, Self::Info, Self::Warn, Self::Error]
    }

    fn to_possible_value(&self) -> Option<PossibleValue> {
        let v = match self {
            Self::Debug => PossibleValue::new("debug"),
            Self::Info => PossibleValue::new("info"),
            Self::Warn => PossibleValue::new("warn"),
            Self::Error => PossibleValue::new("error"),
        };

        Some(v)
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
    let endpoint = cmd.endpoint;

    match cmd.mode {
        ModeCmd::Logging(cmd) => run_logging(cmd, endpoint).await,
    }
}

async fn run_logging(cmd: LoggingCmd, endpoint: String) -> Result<(), Box<dyn Error>> {
    let channel = Channel::from_shared(endpoint)?.connect().await?;
    let mut client = LoggingClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip);

    match cmd {
        LoggingCmd::SetLevel(cmd) => {
            let _ = client
                .update_level(UpdateLevelRequest { level: cmd.level.into() })
                .await?;
        }
    }

    Ok(())
}
