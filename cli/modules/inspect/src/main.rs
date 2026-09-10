//! CLI for YANET "inspect" module.

mod memory;
mod report;

use clap::Parser;
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output,
};
use ynpb::pb::{InspectRequest, InspectResponse, inspect_service_client::InspectServiceClient};

/// The fully-qualified gRPC service name used in error messages.
const INSPECT_SERVICE: &str = "controlplane.ynpb.v1.InspectService";

/// Displays the dataplane introspection report.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    /// Include detailed memory contexts in human output.
    #[arg(long)]
    pub memory: bool,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = InspectService::new(&cmd.globals.connection).await?;
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
            .unary("inspect", InspectRequest {}, async |client, request| {
                client.inspect(request).await
            })
            .await?;

        Ok(response)
    }
}
