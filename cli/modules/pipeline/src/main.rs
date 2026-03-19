//! CLI for YANET "pipeline" module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::CompleteEnv;
use commonpb::pb::{FunctionId, PipelineId};
use serde::Serialize;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};
use ynpb::pb::{
    pipeline_service_client::PipelineServiceClient, DeletePipelineRequest, GetPipelineRequest, ListPipelinesRequest,
    ListPipelinesResponse, Pipeline, UpdatePipelineRequest,
};

/// Pipeline module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List all pipelines.
    List(ListCmd),
    /// Show pipeline definition.
    Show(ShowCmd),
    /// Update pipeline configurations.
    Update(UpdateCmd),
    /// Delete pipeline.
    Delete(DeleteCmd),
}

#[derive(Debug, Clone, Copy, Default, ValueEnum)]
pub enum OutputFormat {
    #[default]
    Yaml,
    Json,
}

#[derive(Debug, Clone, Parser)]
pub struct ListCmd {
    /// Output format.
    #[clap(long, value_enum, default_value_t)]
    pub format: OutputFormat,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Pipeline name.
    #[arg(long)]
    pub name: String,
    /// Output format.
    #[clap(long, value_enum, default_value_t)]
    pub format: OutputFormat,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Pipeline name.
    #[arg(long)]
    pub name: String,
    /// Pipeline functions.
    #[arg(long, value_delimiter = ',')]
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
    let mut service = PipelineService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List(cmd) => {
            let response = service.list_pipelines().await?;
            println_fmt(&response.ids, cmd.format)?;
        }
        ModeCmd::Show(cmd) => {
            let pipeline = service.get_pipeline(&cmd.name).await?;
            println_fmt(&pipeline, cmd.format)?;
        }
        ModeCmd::Update(cmd) => {
            service.update_pipeline(cmd).await?;
            println!("OK");
        }
        ModeCmd::Delete(cmd) => {
            service.delete_pipeline(cmd).await?;
            println!("OK");
        }
    }

    Ok(())
}

fn println_fmt<T>(value: &T, format: OutputFormat) -> Result<(), Box<dyn Error>>
where
    T: Serialize,
{
    match format {
        OutputFormat::Yaml => {
            print!("{}", serde_yaml::to_string(value)?);
        }
        OutputFormat::Json => {
            println!("{}", serde_json::to_string(value)?);
        }
    }

    Ok(())
}

pub struct PipelineService {
    client: PipelineServiceClient<LayeredChannel>,
}

impl PipelineService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = PipelineServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn list_pipelines(&mut self) -> Result<ListPipelinesResponse, Box<dyn Error>> {
        let response = self.client.list(ListPipelinesRequest {}).await?.into_inner();
        Ok(response)
    }

    pub async fn get_pipeline(&mut self, name: &str) -> Result<Option<Pipeline>, Box<dyn Error>> {
        let request = GetPipelineRequest {
            id: Some(PipelineId { name: name.to_string() }),
        };
        let response = self.client.get(request).await?.into_inner();
        Ok(response.pipeline)
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
