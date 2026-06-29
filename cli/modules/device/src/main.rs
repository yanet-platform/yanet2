//! CLI for YANET "device list" command.
//!
//! Lists all configured devices with their registry indices, allowing
//! consumers to resolve numeric device_id values (e.g. from pdump
//! RecordMeta.rx_device_id) to human-readable names.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use colored::Colorize;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{device_service_client::DeviceServiceClient, ListDevicesRequest, ListDevicesResponse};

const DEVICE_SERVICE: &str = "controlplane.ynpb.v1.DeviceService";

/// Device list module - displays all configured devices with their registry
/// indices.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
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

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = DeviceService::new(&cmd.connection).await?;
    let response = service.list().await?;

    output::data(&response, false, format_args!(""), || render(&response));

    Ok(())
}

pub struct DeviceService {
    client: DeviceServiceClient<LayeredChannel>,
    endpoint: String,
}

impl DeviceService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|err| Error::from_connection(err, "device-list", connection.endpoint.clone()))?;
        let client = DeviceServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);

        Ok(Self {
            client,
            endpoint: connection.endpoint.clone(),
        })
    }

    pub async fn list(&mut self) -> Result<ListDevicesResponse, Error> {
        let response = self
            .client
            .list(ListDevicesRequest {})
            .await
            .map_err(|status| Error::from_status(status, "device-list", self.endpoint.clone(), DEVICE_SERVICE))?
            .into_inner();

        Ok(response)
    }
}

fn render(response: &ListDevicesResponse) {
    if response.ids.is_empty() {
        println!("{}", "No devices configured".yellow());
        return;
    }

    println!("{:<8} {:<12} {}", "INDEX".bold(), "TYPE".bold(), "NAME".bold());
    for device in &response.ids {
        println!("{:<8} {:<12} {}", device.index, device.r#type, device.name);
    }
}
