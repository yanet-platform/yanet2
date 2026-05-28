//! CLI for YANET "function" module.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::FunctionId;
use tonic::codec::CompressionEncoding;
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

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Function name.
    #[arg(short, long)]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Function name.
    #[arg(short, long)]
    pub name: String,
    /// Chains in format name:weight=type:name,type:name
    #[arg(long)]
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
    let action = action_name(&cmd.mode);
    let mut service = FunctionService::new(&cmd.connection, action).await?;

    match cmd.mode {
        ModeCmd::List => {
            let ids = service.list_functions().await?;
            output::data(&ids, false, format_args!("No functions found."), || {
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
            output::success("update", format_args!("updated function {}", name));
        }
        ModeCmd::Delete(delete) => {
            let name = delete.name.clone();
            service.delete_function(delete).await?;
            output::success("delete", format_args!("deleted function {}", name));
        }
    }

    Ok(())
}

fn action_name(mode: &ModeCmd) -> &'static str {
    match mode {
        ModeCmd::List => "list functions",
        ModeCmd::Show(..) => "show function",
        ModeCmd::Update(..) => "update function",
        ModeCmd::Delete(..) => "delete function",
    }
}

pub struct FunctionService {
    client: FunctionServiceClient<LayeredChannel>,
    endpoint: String,
}

impl FunctionService {
    pub async fn new(connection: &ConnectionArgs, action: &str) -> Result<Self, Error> {
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
            .map_err(|status| Error::from_status(status, "list functions", self.endpoint.clone(), FUNCTION_SERVICE))?
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
            .map_err(|status| Error::from_status(status, "show function", self.endpoint.clone(), FUNCTION_SERVICE))?
            .into_inner();

        let function = response.function.ok_or_else(|| {
            Error::from_status(
                tonic::Status::not_found(format!("function {} not found", name)),
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
                id: Some(FunctionId { name: cmd.name }),
                chains: cmd.chains,
            }),
        };

        self.client
            .update(request)
            .await
            .map_err(|status| Error::from_status(status, "update function", self.endpoint.clone(), FUNCTION_SERVICE))?;

        Ok(())
    }

    pub async fn delete_function(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteFunctionRequest {
            id: Some(FunctionId { name: cmd.name }),
        };
        self.client
            .delete(request)
            .await
            .map_err(|status| Error::from_status(status, "delete function", self.endpoint.clone(), FUNCTION_SERVICE))?;

        Ok(())
    }
}
