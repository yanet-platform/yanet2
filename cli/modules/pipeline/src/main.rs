//! CLI for YANET "pipeline" module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{pipeline_service_client::PipelineServiceClient, DeletePipelineRequest, Pipeline, UpdatePipelineRequest};
use commonpb::{FunctionId, PipelineId};
use tonic::{codec::CompressionEncoding, transport::Channel};
use ync::logging;

#[allow(non_snake_case)]
pub mod commonpb {
    tonic::include_proto!("commonpb");
}

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
    /// Delete pipeline.
    Delete(DeleteCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Pipeline name.
    #[arg(long)]
    pub name: String,
    /// Pipeline functions.
    #[arg(long)]
    pub functions: Vec<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// Pipeline name.
    #[arg(short, long)]
    pub name: String,
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
        ModeCmd::Update(cmd) => service.update_pipeline(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_pipeline(cmd).await,
    }
}

pub struct PipelineService {
    client: PipelineServiceClient<Channel>,
}

impl PipelineService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let channel = Channel::from_shared(endpoint)?.connect().await?;
        let client = PipelineServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn update_pipeline(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdatePipelineRequest {
            pipeline: Some(Pipeline {
                id: Some(PipelineId { name: cmd.name }),
                functions: cmd
                    .functions
                    .into_iter()
                    .map(|m| FunctionId { name: m.to_string() })
                    .collect(),
            }),
        };

        self.client.update(request).await?;
        log::info!("Successfully updated pipelines");
        Ok(())
    }

    pub async fn delete_pipeline(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeletePipelineRequest {
            id: Some(PipelineId { name: cmd.name }),
        };
        self.client.delete(request).await?;
        log::info!("Successfully deleted pipeline");
        Ok(())
    }
}
