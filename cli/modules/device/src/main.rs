//! CLI for YANET "device list" command.
//!
//! Lists all configured devices with their registry indices, allowing
//! consumers to resolve numeric device_id values (e.g. from pdump
//! RecordMeta.rx_device_id) to human-readable names.

use clap::Parser;
use colored::Colorize;
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    errors::Error,
    output,
};
use ynpb::pb::{ListDevicesRequest, ListDevicesResponse, device_service_client::DeviceServiceClient};

const DEVICE_SERVICE: &str = "controlplane.ynpb.v1.DeviceService";

/// Lists the configured devices with their registry indices.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[command(flatten)]
    pub globals: GlobalArgs,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = Service::connect_for(&cmd.globals.connection, "device-list", DEVICE_SERVICE, |channel| {
        DeviceServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip)
    })
    .await?;
    let response = list(&mut service).await?;

    output::data(|| &response, || render(&response));

    Ok(())
}

type DeviceService = Service<DeviceServiceClient<LayeredChannel>>;

async fn list(service: &mut DeviceService) -> Result<ListDevicesResponse, Error> {
    let response = service
        .unary("device-list", ListDevicesRequest {}, async |client, request| {
            client.list(request).await
        })
        .await?;

    Ok(response)
}

fn render(response: &ListDevicesResponse) {
    if response.ids.is_empty() {
        output::empty(format_args!("No devices found."));
        return;
    }

    println!("{:<8} {:<12} {}", "INDEX".bold(), "TYPE".bold(), "NAME".bold());
    for device in &response.ids {
        println!("{:<8} {:<12} {}", device.index, device.r#type, device.name);
    }
}
