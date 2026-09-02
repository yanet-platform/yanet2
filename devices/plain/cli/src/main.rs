use clap::{ArgAction, Parser};
use commonpb::pb::Device;
use plainpb::{UpdateDevicePlainRequest, device_plain_service_client::DevicePlainServiceClient};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod plainpb {
    use serde::Serialize;

    tonic::include_proto!("devices.plain.controlplane.plainpb.v1");
}

/// DevicePlain module.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    Update(UpdateCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the device.
    #[arg(long, short = 'n')]
    pub name: String,
    /// Pipeline assignments in format "pipeline_name:weight"
    #[arg(long, short = 'i')]
    pub input: Vec<String>,
    /// Pipeline assignments in format "pipeline_name:weight"
    #[arg(long, short = 'o')]
    pub output: Vec<String>,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "devices.plain.controlplane.plainpb.v1.DevicePlainService";

pub struct DevicePlainService {
    service: Service<DevicePlainServiceClient<LayeredChannel>>,
}

impl DevicePlainService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect_for(connection, "update", SERVICE_NAME, |channel| {
            DevicePlainServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let input = cmd
            .input
            .into_iter()
            .map(|s| s.parse::<commonpb::pb::DevicePipeline>())
            .collect::<Result<Vec<_>, _>>()
            .map_err(|err| self.service.invalid("update", err.to_string()))?;
        let output = cmd
            .output
            .into_iter()
            .map(|s| s.parse::<commonpb::pb::DevicePipeline>())
            .collect::<Result<Vec<_>, _>>()
            .map_err(|err| self.service.invalid("update", err.to_string()))?;

        let request = UpdateDevicePlainRequest {
            name: cmd.name.clone(),
            device: Some(Device { input, output }),
        };

        self.service
            .client()
            .update_device(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success("update", format_args!("Updated device {}.", cmd.name));

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = DevicePlainService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
    }
}

pub fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}
