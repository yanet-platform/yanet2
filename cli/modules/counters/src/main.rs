//! CLI for YANET "counters" module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{counters_service_client::CountersServiceClient, PipelineCountersRequest, PipelineModuleCountersRequest};
use tonic::transport::Channel;
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("ynpb");
}

/// Counters module - displays counters information.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Output format.
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Show pipeline counters.
    Pipeline(PipelineCmd),
    /// Show counters of module assigned to a pipeline.
    PipelineModule(PipelineModuleCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct PipelineCmd {
    #[arg(long)]
    pub numa: u32,
    #[arg(long)]
    pub pipeline_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct PipelineModuleCmd {
    #[arg(long)]
    pub numa: u32,
    #[arg(long)]
    pub pipeline_name: String,
    #[arg(long)]
    pub module_type: String,
    #[arg(long)]
    pub module_name: String,
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
    let mut service = CountersService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Pipeline(cmd) => service.show_pipeline(cmd.numa, cmd.pipeline_name).await?,
        ModeCmd::PipelineModule(cmd) => service.show_pipeline_module(cmd.numa, cmd.pipeline_name, cmd.module_type, cmd.module_name).await?,
    }

    Ok(())
}

pub struct CountersService {
    client:CountersServiceClient<Channel>,
}

impl CountersService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = CountersServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    pub async fn show_pipeline(&mut self, numa: u32, pipeline_name: String) -> Result<(), Box<dyn Error>> {
        let request = PipelineCountersRequest {
            numa: numa,
            pipeline: pipeline_name,
        };
        let response = self.client.pipeline(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }

    pub async fn show_pipeline_module(&mut self, numa: u32, pipeline_name: String, module_type: String, module_name: String) -> Result<(), Box<dyn Error>> {
        let request = PipelineModuleCountersRequest {
                numa: numa,
                pipeline: pipeline_name,
                module_type: module_type,
                module_name: module_name,
            };
        let response = self.client.pipeline_module(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }
}
