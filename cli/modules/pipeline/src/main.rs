//! CLI for YANET "pipeline" module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{
    pipeline_service_client::PipelineServiceClient, AssignPipelinesRequest, DevicePipeline, DevicePipelines,
    PipelineChain, PipelineChainNode, UpdatePipelinesRequest,
};
use tonic::transport::Channel;
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    tonic::include_proto!("ynpb");
}

/// Pipeline module.
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
    /// Update pipeline configurations.
    Update(UpdateCmd),
    /// Assign pipelines to devices.
    Assign(AssignCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Dataplane instance where the changes should be applied.
    #[arg(long)]
    pub instance: u32,
    /// Pipeline name.
    #[arg(long)]
    pub name: String,
    /// Module names and their configs in format "module_name:config_name".
    #[arg(long)]
    pub modules: Vec<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct AssignCmd {
    /// Dataplane instance where the changes should be applied.
    #[arg(short, long)]
    pub instance: u32,
    /// Device ID to assign pipelines to.
    #[arg(short, long)]
    pub device: String,
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
    let mut service = PipelineService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Update(cmd) => service.update_pipelines(cmd).await,
        ModeCmd::Assign(cmd) => service.assign_pipeline(cmd).await,
    }
}

pub struct PipelineService {
    client: PipelineServiceClient<Channel>,
}

impl PipelineService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let channel = Channel::from_shared(endpoint)?.connect().await?;
        let client = PipelineServiceClient::new(channel);
        Ok(Self { client })
    }

    pub async fn update_pipelines(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdatePipelinesRequest {
            instance: cmd.instance,
            chains: vec![PipelineChain {
                name: cmd.name,
                nodes: cmd
                    .modules
                    .into_iter()
                    .map(|m| {
                        let parts: Vec<&str> = m.split(':').collect();
                        if parts.len() != 2 {
                            panic!("Invalid module format. Expected 'module_name:config_name'");
                        }
                        PipelineChainNode {
                            module_name: parts[0].to_string(),
                            config_name: parts[1].to_string(),
                        }
                    })
                    .collect(),
            }],
        };

        self.client.update(request).await?;
        log::info!("Successfully updated pipelines");
        Ok(())
    }

    pub async fn assign_pipeline(&mut self, cmd: AssignCmd) -> Result<(), Box<dyn Error>> {
        let device_pipelines = DevicePipelines {
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
                        pipeline_name: parts[0].to_string(),
                        pipeline_weight: weight,
                    }
                })
                .collect(),
        };

        let mut devices = std::collections::HashMap::new();
        devices.insert(cmd.device, device_pipelines);

        let request = AssignPipelinesRequest { instance: cmd.instance, devices };

        self.client.assign(request).await?;
        log::info!("Successfully assigned pipeline to device");
        Ok(())
    }
}
