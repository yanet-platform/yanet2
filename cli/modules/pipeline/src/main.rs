//! CLI for YANET "pipeline" module.

use clap::{CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::{FunctionId, PipelineId};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{ConnectionArgs, LayeredChannel, Service},
    completion, display,
    errors::Error,
    output,
};
use ynpb::pb::{
    DeletePipelineRequest, GetPipelineRequest, ListPipelinesRequest, Pipeline, UpdatePipelineRequest,
    pipeline_service_client::PipelineServiceClient,
};

const PIPELINE_SERVICE: &str = "controlplane.ynpb.v1.PipelineService";

fn client(channel: LayeredChannel) -> PipelineServiceClient<LayeredChannel> {
    PipelineServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages pipelines.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
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
    #[arg(long, short = 'n', add = ArgValueCandidates::new(pipeline_candidates))]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
#[command(after_help = "Examples:\n  yanet-cli pipeline update --name main --functions acl,route")]
pub struct UpdateCmd {
    /// Pipeline name.
    #[arg(long, short = 'n', add = ArgValueCandidates::new(pipeline_candidates))]
    pub name: String,
    /// Pipeline functions.
    #[arg(long, required = true, num_args = 0.., value_delimiter = ',')]
    pub functions: Vec<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// Pipeline name.
    #[arg(long, short = 'n', add = ArgValueCandidates::new(pipeline_candidates))]
    pub name: String,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = PipelineService::new(&cmd.globals.connection, action).await?;

    match cmd.mode {
        ModeCmd::List => {
            let ids = service.list_pipelines().await?;
            let names: Vec<String> = ids.iter().map(|id| id.name.clone()).collect();
            output::data(
                || &ids,
                || display::print_names(&names, format_args!("No pipelines found.")),
            );
        }
        ModeCmd::Show(show) => {
            let pipeline = service.get_pipeline(&show.name).await?;
            output::data(
                || &pipeline,
                || {
                    print!(
                        "{}",
                        serde_yaml::to_string(&pipeline).expect("pipeline YAML serialization must not fail")
                    );

                    if pipeline.functions.is_empty() {
                        output::empty_with_hint(
                            format_args!("No functions found for '{}'.", show.name),
                            format_args!(
                                "create one with 'yanet-cli pipeline update --name <name> --functions <function>'"
                            ),
                        );
                    }
                },
            );
        }
        ModeCmd::Update(update) => {
            let name = update.name.clone();
            service.update_pipeline(update).await?;
            output::success(action, format_args!("Updated pipeline '{name}'."));
        }
        ModeCmd::Delete(delete) => {
            let name = delete.name.clone();
            service.delete_pipeline(delete).await?;
            output::success(action, format_args!("Deleted pipeline '{name}'."));
        }
    }

    Ok(())
}

pub struct PipelineService {
    service: Service<PipelineServiceClient<LayeredChannel>>,
    action: &'static str,
}

impl PipelineService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, PIPELINE_SERVICE, client).await?;

        Ok(Self { service, action })
    }

    pub async fn list_pipelines(&mut self) -> Result<Vec<PipelineId>, Error> {
        let response = self
            .service
            .unary_with(
                self.action,
                ListPipelinesRequest {},
                self.service.not_found(self.action, "pipeline service"),
                async |client, request| client.list(request).await,
            )
            .await?;

        Ok(response.ids)
    }

    pub async fn get_pipeline(&mut self, name: &str) -> Result<Pipeline, Error> {
        let request = GetPipelineRequest {
            id: Some(PipelineId { name: name.to_string() }),
        };
        let response = self
            .service
            .unary_with(
                self.action,
                request,
                self.service.not_found(self.action, &format!("pipeline '{name}'")),
                async |client, request| client.get(request).await,
            )
            .await?;

        let pipeline = response.pipeline.ok_or_else(|| {
            Error::from_status(
                tonic::Status::not_found(format!("pipeline {name} not found")),
                self.action,
                self.service.endpoint(),
                PIPELINE_SERVICE,
            )
        })?;

        Ok(pipeline)
    }

    pub async fn update_pipeline(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let pipeline_name = cmd.name;
        let request = UpdatePipelineRequest {
            pipeline: Some(Pipeline {
                id: Some(PipelineId { name: pipeline_name.clone() }),
                functions: cmd
                    .functions
                    .into_iter()
                    .map(|m| FunctionId { name: m.to_string() })
                    .collect(),
            }),
        };

        // Update is an upsert, so a referenced function the live configuration
        // cannot resolve is a failed precondition, never a missing pipeline.
        //
        // The backend message is kept verbatim.
        self.service
            .unary(self.action, request, async |client, request| {
                client.update(request).await
            })
            .await?;

        Ok(())
    }

    pub async fn delete_pipeline(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeletePipelineRequest {
            id: Some(PipelineId { name: cmd.name }),
        };

        let name = request.id.as_ref().expect("pipeline id").name.clone();
        self.service
            .unary_with(
                self.action,
                request,
                self.service.not_found(self.action, &format!("pipeline '{name}'")),
                async |client, request| client.delete(request).await,
            )
            .await?;

        Ok(())
    }
}

/// Completion candidates for a `--name` argument: the pipelines the module
/// currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn pipeline_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client
            .list(ListPipelinesRequest {})
            .await?
            .into_inner()
            .ids
            .into_iter()
            .map(|id| id.name)
            .collect())
    })
}
