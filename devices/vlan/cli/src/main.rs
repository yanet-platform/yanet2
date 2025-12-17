use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::CompleteEnv;
use code::{UpdateDeviceVlanRequest, device_vlan_service_client::DeviceVlanServiceClient};
use commonpb::{Device, DevicePipeline, TargetDevice};
use tonic::transport::Channel;
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("vlanpb");
}

#[allow(non_snake_case)]
pub mod commonpb {
    use serde::Serialize;

    tonic::include_proto!("commonpb");
}

/// DeviceVlan module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

/// Output format options.
#[derive(Debug, Clone, ValueEnum)]
pub enum OutputFormat {
    /// Tree structure with colored output (default).
    Tree,
    /// JSON format.
    Json,
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
    /// Vlan tag
    #[arg(short, long)]
    pub vlan: u16,
}

pub struct DeviceVlanService {
    client: DeviceVlanServiceClient<Channel>,
}

impl DeviceVlanService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = DeviceVlanServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdateDeviceVlanRequest {
            target: Some(TargetDevice {
                name: cmd.name,
            }),
            device: Some(Device {
                input: cmd
                    .input
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
                output: cmd
                    .output
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
            }),
            vlan: cmd.vlan as u32,
        };

        self.client.update_device(request).await?;
        log::info!("Successfully updated device");

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = DeviceVlanService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
    }
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("initialize logging");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}
