//! CLI for YANET "pipeline" module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{
    device_service_client::DeviceServiceClient, Device, DevicePipeline, UpdateDevicesRequest,
};
use tonic::transport::Channel;
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    tonic::include_proto!("ynpb");
}

/// Device module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Update device configurations.
    Update(UpdateCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Dataplane instance where the changes should be applied.
    #[arg(long)]
    pub instance: u32,
    /// Device name.
    #[arg(long)]
    pub name: String,
    /// Device Id
    #[arg(long)]
    pub device_id: u32,
    /// Vlan
    #[arg(long)]
    pub vlan: u32,
    /// Pipeline assignments in format "pipeline_name:weight"
    #[arg(short, long)]
    pub pipelines: Vec<String>,
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("no error expected");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = DeviceService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Update(cmd) => service.update_devices(cmd).await,
    }
}

pub struct DeviceService {
    client: DeviceServiceClient<Channel>,
}

impl DeviceService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let channel = Channel::from_shared(endpoint)?.connect().await?;
        let client = DeviceServiceClient::new(channel);
        Ok(Self { client })
    }

    pub async fn update_devices(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdateDevicesRequest {
            instance: cmd.instance,
            devices: vec![Device {
                name: cmd.name,
                device_id: cmd.device_id,
                vlan: cmd.vlan,
                pipelines: cmd
                    .pipelines
                    .into_iter()
                    .map(|p| {
                        let parts: Vec<&str> = p.split(':').collect();
                        if parts.len() != 2 {
                            panic!("Invalid pipeline format. Expected 'pipeline_name:weight'");
                        }
                        let weight = parts[1].parse::<u64>().expect("Invalid weight value");
                        DevicePipeline {
                            name: parts[0].to_string(),
                            weight: weight,
                        }
                    })
                    .collect(),
            }],
        };

        self.client.update(request).await?;
        log::info!("Successfully updated devices");
        Ok(())
    }
}
