//! CLI for YANET "device delete" command.

use clap::{CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion,
    errors::Error,
    output,
};
use ynpb::pb::{DeleteDeviceRequest, ListDevicesRequest, device_service_client::DeviceServiceClient};

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "controlplane.ynpb.v1.DeviceService";

fn client(channel: LayeredChannel) -> DeviceServiceClient<LayeredChannel> {
    DeviceServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Deletes a configured device.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    /// Name of the device to delete.
    #[arg(add = ArgValueCandidates::new(device_candidates))]
    pub name: String,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = "delete";
    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    let name = cmd.name;
    service
        .unary_with(
            action,
            DeleteDeviceRequest { name: name.clone() },
            service.not_found(action, &format!("device '{name}'")),
            async |client, request| client.delete(request).await,
        )
        .await?;

    output::success(action, format_args!("Deleted device '{name}'."));

    Ok(())
}

/// Completion candidates for the device-name argument: the devices the
/// service currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn device_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client
            .list(ListDevicesRequest {})
            .await?
            .into_inner()
            .ids
            .into_iter()
            .map(|id| id.name)
            .collect())
    })
}
