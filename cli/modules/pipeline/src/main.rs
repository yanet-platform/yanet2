//! CLI for YANET "pipeline" module.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::{FunctionId, PipelineId};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{
    pipeline_service_client::PipelineServiceClient, DeletePipelineRequest, GetPipelineRequest, ListPipelinesRequest,
    Pipeline, UpdatePipelineRequest,
};

const PIPELINE_SERVICE: &str = "ynpb.PipelineService";

/// Pipeline module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List all pipelines.
    List,
    /// Show pipeline definition.
    Show(ShowCmd),
    /// Update pipeline configurations.
    Update(UpdateCmd),
    /// Delete pipeline.
    Delete(DeleteCmd),
}

impl ModeCmd {
    pub fn action(&self) -> &'static str {
        match self {
            ModeCmd::List => "list pipelines",
            ModeCmd::Show(..) => "show pipeline",
            ModeCmd::Update(..) => "update pipeline",
            ModeCmd::Delete(..) => "delete pipeline",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Pipeline name.
    #[arg(short, long)]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Pipeline name.
    #[arg(short, long)]
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
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = PipelineService::new(&cmd.connection, action).await?;

    match cmd.mode {
        ModeCmd::List => {
            let ids = service.list_pipelines().await?;
            output::data(&ids, ids.is_empty(), format_args!("no pipelines configured"), || {
                print!(
                    "{}",
                    serde_yaml::to_string(&ids).expect("pipeline list YAML serialization must not fail")
                );
            });
        }
        ModeCmd::Show(show) => {
            let pipeline = service.get_pipeline(&show.name).await?;
            output::data(&pipeline, false, format_args!(""), || {
                print!(
                    "{}",
                    serde_yaml::to_string(&pipeline).expect("pipeline YAML serialization must not fail")
                );
            });
        }
        ModeCmd::Update(update) => {
            let name = update.name.clone();
            service.update_pipeline(update).await?;
            output::success(action, format_args!("updated pipeline {name}"));
        }
        ModeCmd::Delete(delete) => {
            let name = delete.name.clone();
            service.delete_pipeline(delete).await?;
            output::success(action, format_args!("deleted pipeline {name}"));
        }
    }

    Ok(())
}

pub struct PipelineService {
    client: PipelineServiceClient<LayeredChannel>,
    endpoint: String,
    action: &'static str,
}

impl PipelineService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|err| Error::from_connection(err, action, connection.endpoint.clone()))?;
        let client = PipelineServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);

        Ok(Self {
            client,
            endpoint: connection.endpoint.clone(),
            action,
        })
    }

    fn map_err(&self) -> impl FnOnce(tonic::Status) -> Error + '_ {
        let endpoint = self.endpoint.clone();
        let action = self.action;
        move |status| Error::from_status(status, action, endpoint, PIPELINE_SERVICE)
    }

    pub async fn list_pipelines(&mut self) -> Result<Vec<PipelineId>, Error> {
        let response = self
            .client
            .list(ListPipelinesRequest {})
            .await
            .map_err(self.map_err())?
            .into_inner();

        Ok(response.ids)
    }

    pub async fn get_pipeline(&mut self, name: &str) -> Result<Pipeline, Error> {
        let request = GetPipelineRequest {
            id: Some(PipelineId { name: name.to_string() }),
        };
        let response = self.client.get(request).await.map_err(self.map_err())?.into_inner();

        let pipeline = response.pipeline.ok_or_else(|| {
            Error::from_status(
                tonic::Status::not_found(format!("pipeline {name} not found")),
                self.action,
                self.endpoint.clone(),
                PIPELINE_SERVICE,
            )
        })?;

        Ok(pipeline)
    }

    pub async fn update_pipeline(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
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

        self.client.update(request).await.map_err(self.map_err())?;

        Ok(())
    }

    pub async fn delete_pipeline(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeletePipelineRequest {
            id: Some(PipelineId { name: cmd.name }),
        };

        self.client.delete(request).await.map_err(self.map_err())?;

        Ok(())
    }
}
