use core::{error::Error, str::FromStr};

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::CompleteEnv;
use code::{UpdateDevicePlainRequest, device_plain_service_client::DevicePlainServiceClient};
use commonpb::{Device, DevicePipeline};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("plainpb");
}

#[allow(non_snake_case)]
pub mod commonpb {
    use serde::Serialize;

    tonic::include_proto!("commonpb");
}

/// DevicePlain module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
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
    /// The name of the device.
    #[arg(long, short)]
    pub name: String,
    /// Pipeline assignments in format "pipeline_name:weight"
    #[arg(short, long)]
    pub input: Vec<String>,
    /// Pipeline assignments in format "pipeline_name:weight"
    #[arg(short, long)]
    pub output: Vec<String>,
}

impl FromStr for DevicePipeline {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let (name, weight) = s
            .split_once(':')
            .ok_or_else(|| format!("invalid pipeline format '{s}': expected 'name:weight'"))?;
        let weight = weight
            .parse::<u64>()
            .map_err(|e| format!("invalid weight in '{s}': {e}"))?;
        Ok(DevicePipeline { name: name.to_string(), weight })
    }
}

pub struct DevicePlainService {
    client: DevicePlainServiceClient<LayeredChannel>,
}

impl DevicePlainService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = DevicePlainServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdateDevicePlainRequest {
            name: cmd.name,
            device: Some(Device {
                input: cmd
                    .input
                    .into_iter()
                    .map(|s| s.parse())
                    .collect::<Result<Vec<_>, _>>()?,
                output: cmd
                    .output
                    .into_iter()
                    .map(|s| s.parse())
                    .collect::<Result<Vec<_>, _>>()?,
            }),
        };

        self.client.update_device(request).await?;
        log::info!("Successfully updated device");

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = DevicePlainService::new(&cmd.connection).await?;

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
