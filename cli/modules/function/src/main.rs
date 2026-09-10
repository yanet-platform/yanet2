//! CLI for YANET "function" module.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::FunctionId;
use tonic::{Status, codec::CompressionEncoding};
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    completion, display,
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{
    DeleteFunctionRequest, Function, FunctionChain, GetFunctionRequest, ListFunctionsRequest, UpdateFunctionRequest,
    function_service_client::FunctionServiceClient,
};

const FUNCTION_SERVICE: &str = "controlplane.ynpb.v1.FunctionService";

fn client(channel: LayeredChannel) -> FunctionServiceClient<LayeredChannel> {
    FunctionServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages functions.
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
    /// Be verbose: shows debug log lines and raw gRPC error details.
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
            let names: Vec<String> = ids.iter().map(|id| id.name.clone()).collect();
            output::data(
                || &ids,
                || display::print_names(&names, format_args!("No functions found.")),
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
                            format_args!(
                                "create one with 'yanet-cli function update --name <name> --chains <name:weight=type:name>'"
                            ),
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
        let service = Service::connect_for(connection, action, FUNCTION_SERVICE, client).await?;

        Ok(Self { service })
    }

    pub async fn list_functions(&mut self) -> Result<Vec<FunctionId>, Error> {
        let response = self
            .service
            .unary_with(
                "list functions",
                ListFunctionsRequest {},
                self.service.not_found("list functions", "requested function"),
                async |client, request| client.list(request).await,
            )
            .await?;

        Ok(response.ids)
    }

    pub async fn get_function(&mut self, name: &str) -> Result<Function, Error> {
        let request = GetFunctionRequest {
            id: Some(FunctionId { name: name.to_string() }),
        };

        let response = self
            .service
            .unary_with(
                "show function",
                request,
                self.service.not_found("show function", &format!("function '{name}'")),
                async |client, request| client.get(request).await,
            )
            .await?;

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

        // Update is an upsert, so a chain module the live configuration cannot
        // resolve is a failed precondition, never a missing function.
        //
        // The backend message is kept verbatim.
        self.service
            .unary("update function", request, async |client, request| {
                client.update(request).await
            })
            .await?;

        Ok(())
    }

    pub async fn delete_function(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let name = cmd.name;

        let request = DeleteFunctionRequest {
            id: Some(FunctionId { name: name.clone() }),
        };
        self.service
            .unary_with(
                "delete function",
                request,
                self.service.not_found("delete function", &format!("function '{name}'")),
                async |client, request| client.delete(request).await,
            )
            .await?;

        Ok(())
    }
}

/// Completion candidates for a `--name` argument: the functions the module
/// currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn function_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client
            .list(ListFunctionsRequest {})
            .await?
            .into_inner()
            .ids
            .into_iter()
            .map(|id| id.name)
            .collect())
    })
}
