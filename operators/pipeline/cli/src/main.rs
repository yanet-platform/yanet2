//! CLI for YANET pipeline operator.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::GetMetricsRequest;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
    readiness,
};

use crate::operatorpb::{
    metrics_service_client::MetricsServiceClient, readiness_service_client::ReadinessServiceClient,
};

#[allow(clippy::all, non_snake_case)]
pub mod operatorpb {
    tonic::include_proto!("operators.pipeline.operatorpb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "operators.pipeline.operatorpb.v1.ReadinessService";

/// The fully-qualified metrics service name used in error messages.
const METRICS_SERVICE_NAME: &str = "operators.pipeline.operatorpb.v1.MetricsService";

/// Exit code used when the RPC succeeds but not all scopes are `STATE_READY`.
const EXIT_NOT_READY: i32 = 2;

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
    /// Show per-scope readiness of the pipeline operator.
    Ready(ReadyCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ReadyCmd {
    /// Restrict output to these scope names; empty means all.
    pub scopes: Vec<String>,
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();

    ync::init(cmd.verbose, cmd.format);

    match run(cmd).await {
        Ok(true) => {}
        Ok(false) => std::process::exit(EXIT_NOT_READY),
        Err(err) => {
            output::failure(&err);
            std::process::exit(err.exit_code());
        }
    }
}

/// Run the requested subcommand.
///
/// Returns `Ok(true)` when the subcommand succeeded (and, for `ready`,
/// every returned scope is `STATE_READY`), `Ok(false)` when `ready` succeeded
/// but at least one scope is not ready, and `Err(_)` on transport or RPC
/// failure.
async fn run(cmd: Cmd) -> Result<bool, Error> {
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

            Ok(true)
        }
        ModeCmd::Ready(ready_cmd) => {
            let mut service = PipelineReadinessService::new(&cmd.connection).await?;
            service.ready(ready_cmd).await
        }
    }
}

pub struct PipelineReadinessService {
    service: Service<ReadinessServiceClient<LayeredChannel>>,
}

impl PipelineReadinessService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            ReadinessServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn ready(&mut self, cmd: ReadyCmd) -> Result<bool, Error> {
        let request = readinesspb::pb::ReadyRequest { scopes: cmd.scopes.clone() };

        let response = self
            .service
            .client()
            .ready(request)
            .await
            .map_err(self.service.status("ready"))?
            .into_inner();

        Ok(readiness::report(&response.scopes, &cmd.scopes))
    }
}
