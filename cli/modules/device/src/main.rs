//! CLI for YANET "device list" command.
//!
//! Lists all configured devices with their registry indices, allowing
//! consumers to resolve numeric device_id values (e.g. from pdump
//! RecordMeta.rx_device_id) to human-readable names.

use clap::{ArgAction, Parser};
use colored::Colorize;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{device_service_client::DeviceServiceClient, ListDevicesRequest, ListDevicesResponse};

const DEVICE_SERVICE: &str = "controlplane.ynpb.v1.DeviceService";

/// Device list module - displays all configured devices with their registry
/// indices.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

pub fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = DeviceService::new(&cmd.connection).await?;
    let response = service.list().await?;

    output::data(|| &response, || render(&response));

    Ok(())
}

pub struct DeviceService {
    service: Service<DeviceServiceClient<LayeredChannel>>,
}

impl DeviceService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect_for(connection, "device-list", DEVICE_SERVICE, |channel| {
            DeviceServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn list(&mut self) -> Result<ListDevicesResponse, Error> {
        let response = self
            .service
            .client()
            .list(ListDevicesRequest {})
            .await
            .map_err(self.service.status("device-list"))?
            .into_inner();

        Ok(response)
    }
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
