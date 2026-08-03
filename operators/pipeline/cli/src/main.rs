//! CLI for YANET pipeline operator.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::GetMetricsRequest;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, Service},
    errors::Error,
    output::{self, CommonFormat},
};

use crate::operatorpb::metrics_service_client::MetricsServiceClient;

#[allow(clippy::all, clippy::std_instead_of_core, non_snake_case)]
pub mod operatorpb {
    tonic::include_proto!("operators.pipeline.operatorpb.v1");
}

/// The fully-qualified metrics service name used in error messages.
const METRICS_SERVICE_NAME: &str = "operators.pipeline.operatorpb.v1.MetricsService";

/// Pipeline operator CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Show operator metrics.
    Metrics,
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();

    ync::init(cmd.verbose, cmd.format);

    match run(cmd).await {
        Ok(()) => {}
        Err(err) => {
            output::failure(&err);
            std::process::exit(err.exit_code());
        }
    }
}

/// Run the requested subcommand.
///
/// Returns `Ok(())` when the metrics RPC succeeded, `Err(_)` on transport or
/// RPC failure.
async fn run(cmd: Cmd) -> Result<(), Error> {
    match cmd.mode {
        ModeCmd::Metrics => {
            let mut service = Service::connect(&cmd.connection, METRICS_SERVICE_NAME, |channel| {
                MetricsServiceClient::new(channel)
                    .send_compressed(CompressionEncoding::Gzip)
                    .accept_compressed(CompressionEncoding::Gzip)
            })
            .await?;

            let response = service
                .client()
                .get_metrics(GetMetricsRequest::default())
                .await
                .map_err(service.status("get_metrics"))?
                .into_inner();

            let data = serde_json::to_string(&response).expect("metrics serialization must not fail");
            println!("{data}");

            Ok(())
        }
    }
}
