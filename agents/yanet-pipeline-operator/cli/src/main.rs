//! CLI for YANET pipeline operator.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use ync::{client::ConnectionArgs, logging};

use crate::operatorpb::{GetMetricsRequest, metrics_service_client::MetricsServiceClient};

#[allow(non_snake_case)]
pub mod operatorpb {
    tonic::include_proto!("operatorpb");
}

#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
enum ModeCmd {
    /// Show operator metrics.
    Metrics,
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    let _ = logging::init(cmd.verbose as usize);

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let channel = ync::client::connect(&cmd.connection).await?;
    let mut service = MetricsServiceClient::new(channel);

    match cmd.mode {
        ModeCmd::Metrics => {
            let response = service
                .get_metrics(GetMetricsRequest {})
                .await
                .map_err(|error| -> Box<dyn Error> { Box::new(error) })?
                .into_inner();

            let data = serde_json::to_string(&response)?;
            println!("{data}");
        }
    }

    Ok(())
}
