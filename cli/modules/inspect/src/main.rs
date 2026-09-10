//! CLI for YANET "inspect" module.

mod memory;
mod report;

use clap::Parser;
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
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
    let mut service = Service::connect_for(&cmd.globals.connection, "inspect", INSPECT_SERVICE, |channel| {
        InspectServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip)
    })
    .await?;
    let response = inspect(&mut service).await?;

    output::data(|| &response, || report::render(&response, cmd.memory));

    Ok(())
}

type InspectService = Service<InspectServiceClient<LayeredChannel>>;

async fn inspect(service: &mut InspectService) -> Result<InspectResponse, Error> {
    let response = service
        .unary("inspect", InspectRequest {}, async |client, request| {
            client.inspect(request).await
        })
        .await?;

    Ok(response)
}
