//! CLI for YANET "inspect" module.

mod memory;
mod report;

use clap::{ArgAction, Parser};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{InspectRequest, InspectResponse, inspect_service_client::InspectServiceClient};

/// The fully-qualified gRPC service name used in error messages.
const INSPECT_SERVICE: &str = "controlplane.ynpb.v1.InspectService";

/// Inspect module - displays system introspection information.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Include detailed memory contexts in human output.
    #[arg(long)]
    pub memory: bool,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = InspectService::new(&cmd.connection).await?;
    let response = service.inspect().await?;

    output::data(|| &response, || report::render(&response, cmd.memory));

    Ok(())
}

pub struct InspectService {
    service: Service<InspectServiceClient<LayeredChannel>>,
}

impl InspectService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect_for(connection, "inspect", INSPECT_SERVICE, |channel| {
            InspectServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn inspect(&mut self) -> Result<InspectResponse, Error> {
        let response = self
            .service
            .client()
            .inspect(InspectRequest {})
            .await
            .map_err(self.service.status("inspect"))?
            .into_inner();

        Ok(response)
    }
}
