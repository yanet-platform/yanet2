use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{UpdateDeviceVxlanRequest, device_vxlan_service_client::DeviceVxlanServiceClient};
use commonpb::pb::Device;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("devices.vxlan.controlplane.vxlanpb.v1");
}

/// DeviceVxlan module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
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
    /// The name of the device
    #[arg(long, short)]
    pub name: String,
    /// Pipeline assignments in format "pipeline_name:weight"
    #[arg(short, long)]
    pub input: Vec<String>,
    /// Pipeline assignments in format "pipeline_name:weight"
    #[arg(short, long)]
    pub output: Vec<String>,
    /// VXLAN network identifier
    #[arg(short, long)]
    pub vni: u32,
    /// Outer UDP destination port
    #[arg(long)]
    pub dst_port: u32,
    /// Outer source MAC in aa:bb:cc:dd:ee:ff notation
    #[arg(long)]
    pub src_mac: String,
    /// Outer destination MAC in aa:bb:cc:dd:ee:ff notation
    #[arg(long)]
    pub dst_mac: String,
    /// Outer source IPv4 underlay address
    #[arg(long)]
    pub src_ip: String,
    /// Outer destination IPv4 underlay address
    #[arg(long)]
    pub dst_ip: String,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "devices.vxlan.controlplane.vxlanpb.v1.DeviceVxlanService";

pub struct DeviceVxlanService {
    service: Service<DeviceVxlanServiceClient<LayeredChannel>>,
}

impl DeviceVxlanService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            DeviceVxlanServiceClient::new(channel)
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

        let request = UpdateDeviceVxlanRequest {
            name: cmd.name.clone(),
            device: Some(Device { input, output }),
            vni: cmd.vni,
            dst_port: cmd.dst_port,
            src_mac: cmd.src_mac,
            dst_mac: cmd.dst_mac,
            src_ip: cmd.src_ip,
            dst_ip: cmd.dst_ip,
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
    let mut service = DeviceVxlanService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
    }
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
