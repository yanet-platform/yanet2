//! CLI for YANET "function" module.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::FunctionId;
use tonic::{codec::CompressionEncoding, Status};
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::{Error, NotFoundMapper},
    output::{self, CommonFormat},
};
use ynpb::pb::{
    function_service_client::FunctionServiceClient, DeleteFunctionRequest, Function, FunctionChain, GetFunctionRequest,
    ListFunctionsRequest, UpdateFunctionRequest,
};

const FUNCTION_SERVICE: &str = "controlplane.ynpb.v1.FunctionService";
const NOT_FOUND: NotFoundMapper = NotFoundMapper::new(FUNCTION_SERVICE, "requested function");

/// Function module.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
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
    /// List all functions.
    List,
    /// Show function definition.
    Show(ShowCmd),
    /// Update function configurations.
    Update(UpdateCmd),
    /// Delete function.
    Delete(DeleteCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::List => "list functions",
            Self::Show(..) => "show function",
            Self::Update(..) => "update function",
            Self::Delete(..) => "delete function",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Function name.
    #[arg(long, short = 'n', add = ArgValueCandidates::new(function_candidates))]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
#[command(
    about = "Update function configuration.",
    after_help = "Examples:\n  yanet-cli function update --name my-function \\\n      --chains edge:20=filter:acl,route:ipv4 \\\n      --chains control:10=counter:rx"
)]
pub struct UpdateCmd {
    /// Function name.
    #[arg(long, short = 'n', add = ArgValueCandidates::new(function_candidates))]
    pub name: String,
    /// Chains in format `name:weight=type:name,type:name`.
    #[arg(long, required = true)]
    pub chains: Vec<FunctionChain>,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// Function name.
    #[arg(long, short = 'n', add = ArgValueCandidates::new(function_candidates))]
    pub name: String,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = FunctionService::new(&cmd.connection, action).await?;

    match cmd.mode {
        ModeCmd::List => {
            let ids = service.list_functions().await?;
            output::data(
                || &ids,
                || {
                    if ids.is_empty() {
                        output::empty(format_args!("No functions found."));
                        return;
                    }

                    print!(
                        "{}",
                        serde_yaml::to_string(&ids).expect("function list YAML serialization must not fail")
                    );
                },
            );
        }
        ModeCmd::Show(show) => {
            let function = service.get_function(&show.name).await?;
            output::data(
                || &function,
                || {
                    print!(
                        "{}",
                        serde_yaml::to_string(&function).expect("function YAML serialization must not fail")
                    );

                    if function.chains.is_empty() {
                        output::empty_with_hint(
                            format_args!("No chains found for '{}'.", show.name),
                            format_args!("create one with 'yanet-cli function update --name <name> --chains <chain>'"),
                        );
                    }
                },
            );
        }
        ModeCmd::Update(update) => {
            let name = update.name.clone();
            service.update_function(update).await?;
            output::success("update function", format_args!("Updated function '{name}'."));
        }
        ModeCmd::Delete(delete) => {
            let name = delete.name.clone();
            service.delete_function(delete).await?;
            output::success("delete function", format_args!("Deleted function '{name}'."));
        }
    }

    Ok(())
}

pub struct FunctionService {
    service: Service<FunctionServiceClient<LayeredChannel>>,
}

impl FunctionService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, FUNCTION_SERVICE, |channel| {
            FunctionServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn list_functions(&mut self) -> Result<Vec<FunctionId>, Error> {
        let response = self
            .service
            .client()
            .list(ListFunctionsRequest {})
            .await
            .map_err(|status| NOT_FOUND.map(status, "list functions", self.service.endpoint(), None))?
            .into_inner();

        Ok(response.ids)
    }

    pub async fn get_function(&mut self, name: &str) -> Result<Function, Error> {
        let request = GetFunctionRequest {
            id: Some(FunctionId { name: name.to_string() }),
        };

        let response = self
            .service
            .client()
            .get(request)
            .await
            .map_err(|status| {
                NOT_FOUND.map(
                    status,
                    "show function",
                    self.service.endpoint(),
                    Some(&format!("function '{name}'")),
                )
            })?
            .into_inner();

        let function = response.function.ok_or_else(|| {
            Error::from_status(
                Status::not_found(format!("function '{name}' not found")),
                "show function",
                self.service.endpoint(),
                FUNCTION_SERVICE,
            )
        })?;

        Ok(function)
    }

    pub async fn update_function(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let request = UpdateFunctionRequest {
            function: Some(Function {
                id: Some(FunctionId { name: cmd.name.clone() }),
                chains: cmd.chains,
            }),
        };

        // Update is an upsert, so a resource-level NotFound names a referenced
        // chain module, not the function. Keep the backend message verbatim.
        self.service
            .client()
            .update(request)
            .await
            .map_err(self.service.status("update function"))?;

        Ok(())
    }

    pub async fn delete_function(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let name = cmd.name;

        self.service
            .client()
            .delete(DeleteFunctionRequest {
                id: Some(FunctionId { name: name.clone() }),
            })
            .await
            .map_err(|status| {
                NOT_FOUND.map(
                    status,
                    "delete function",
                    self.service.endpoint(),
                    Some(&format!("function '{name}'")),
                )
            })?;

        Ok(())
    }
}

/// Completion candidates for a `--name` argument: the functions the module
/// currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn function_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            FunctionServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| {
            Ok(client
                .list(ListFunctionsRequest {})
                .await?
                .into_inner()
                .ids
                .into_iter()
                .map(|id| id.name)
                .collect())
        },
    )
}
