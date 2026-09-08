//! CLI for YANET "device delete" command.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, Service},
    completion,
    errors::{Error, NotFoundMapper},
    output::{self, CommonFormat},
};
use ynpb::pb::{DeleteDeviceRequest, ListDevicesRequest, device_service_client::DeviceServiceClient};

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "controlplane.ynpb.v1.DeviceService";

/// Maps a genuine "device not found" status into a friendly message.
const NOT_FOUND: NotFoundMapper = NotFoundMapper::new(SERVICE_NAME, "device");

/// Deletes a configured device.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    /// Name of the device to delete.
    #[arg(add = ArgValueCandidates::new(device_candidates))]
    pub name: String,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = "delete";
    let mut service = Service::connect_for(&cmd.connection, action, SERVICE_NAME, |channel| {
        DeviceServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip)
    })
    .await?;

    let name = cmd.name;
    service
        .client()
        .delete(DeleteDeviceRequest { name: name.clone() })
        .await
        .map_err(|status| NOT_FOUND.map(status, action, service.endpoint(), Some(&format!("device '{name}'"))))?;

    output::success(action, format_args!("Deleted device '{name}'."));

    Ok(())
}

/// Completion candidates for the device-name argument: the devices the
/// service currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn device_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            DeviceServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| {
            Ok(client
                .list(ListDevicesRequest {})
                .await?
                .into_inner()
                .ids
                .into_iter()
                .map(|id| id.name)
                .collect())
        },
    )
}
