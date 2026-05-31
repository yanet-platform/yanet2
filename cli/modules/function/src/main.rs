//! CLI for YANET "function" module.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::FunctionId;
use tonic::{codec::CompressionEncoding, Status};
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    errors::Error,
    output::{self, CommonFormat},
};
use ynpb::pb::{
    function_service_client::FunctionServiceClient, DeleteFunctionRequest, Function, FunctionChain, GetFunctionRequest,
    ListFunctionsRequest, UpdateFunctionRequest,
};

const FUNCTION_SERVICE: &str = "ynpb.FunctionService";

/// Function module.
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
    pub fn action(&self) -> &'static str {
        match self {
            ModeCmd::List => "list functions",
            ModeCmd::Show(..) => "show function",
            ModeCmd::Update(..) => "update function",
            ModeCmd::Delete(..) => "delete function",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Function name.
    #[arg(short, long)]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
#[command(
    about = "Update function configuration.",
    after_help = "Examples:\n  yanet-cli function update --name my-function \\\n      --chains edge:20=filter:acl,route:ipv4 \\\n      --chains control:10=counter:rx"
)]
pub struct UpdateCmd {
    /// Function name.
    #[arg(short, long)]
    pub name: String,
    /// Chains in format `name:weight=type:name,type:name`.
    #[arg(long, required = true)]
    pub chains: Vec<FunctionChain>,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// Function name.
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
    let mut service = FunctionService::new(&cmd.connection, action).await?;

    match cmd.mode {
        ModeCmd::List => {
            let ids = service.list_functions().await?;
            output::data(&ids, ids.is_empty(), format_args!("No functions found."), || {
                print!(
                    "{}",
                    serde_yaml::to_string(&ids).expect("function list YAML serialization must not fail")
                );
            });
        }
        ModeCmd::Show(show) => {
            let function = service.get_function(&show.name).await?;
            output::data(&function, false, format_args!(""), || {
                print!(
                    "{}",
                    serde_yaml::to_string(&function).expect("function YAML serialization must not fail")
                );
            });
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

fn map_not_found(status: Status, action: &'static str, endpoint: &str, resource: Option<&str>) -> Error {
    if status.message().trim().eq_ignore_ascii_case("not found") {
        let resource = resource.unwrap_or("requested function");

        return Error::from_status(
            Status::not_found(format!("{resource} not found")),
            action,
            endpoint.to_owned(),
            FUNCTION_SERVICE,
        );
    }

    Error::from_status(status, action, endpoint.to_owned(), FUNCTION_SERVICE)
}

pub struct FunctionService {
    client: FunctionServiceClient<LayeredChannel>,
    endpoint: String,
}

impl FunctionService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|err| Error::from_connection(err, action, connection.endpoint.clone()))?;
        let client = FunctionServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);

        Ok(Self {
            client,
            endpoint: connection.endpoint.clone(),
        })
    }

    pub async fn list_functions(&mut self) -> Result<Vec<FunctionId>, Error> {
        let response = self
            .client
            .list(ListFunctionsRequest {})
            .await
            .map_err(|status| map_not_found(status, "list functions", &self.endpoint, None))?
            .into_inner();

        Ok(response.ids)
    }

    pub async fn get_function(&mut self, name: &str) -> Result<Function, Error> {
        let request = GetFunctionRequest {
            id: Some(FunctionId { name: name.to_string() }),
        };

        let response = self
            .client
            .get(request)
            .await
            .map_err(|status| {
                map_not_found(
                    status,
                    "show function",
                    &self.endpoint,
                    Some(&format!("function '{name}'")),
                )
            })?
            .into_inner();

        let function = response.function.ok_or_else(|| {
            Error::from_status(
                Status::not_found(format!("function '{name}' not found")),
                "show function",
                self.endpoint.clone(),
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

        self.client.update(request).await.map_err(|status| {
            map_not_found(
                status,
                "update function",
                &self.endpoint,
                Some(&format!("function '{name}'", name = cmd.name)),
            )
        })?;

        Ok(())
    }

    pub async fn delete_function(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let name = cmd.name;

        self.client
            .delete(DeleteFunctionRequest {
                id: Some(FunctionId { name: name.clone() }),
            })
            .await
            .map_err(|status| {
                map_not_found(
                    status,
                    "delete function",
                    &self.endpoint,
                    Some(&format!("function '{name}'")),
                )
            })?;

        Ok(())
    }
}
