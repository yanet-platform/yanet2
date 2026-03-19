//! CLI for YANET "function" module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::CompleteEnv;
use commonpb::pb::FunctionId;
use serde::Serialize;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};
use ynpb::pb::{
    function_service_client::FunctionServiceClient, DeleteFunctionRequest, Function, FunctionChain, GetFunctionRequest,
    ListFunctionsRequest, ListFunctionsResponse, UpdateFunctionRequest,
};

/// Function module.
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
    /// List all functions.
    List(ListCmd),
    /// Show function definition.
    Show(ShowCmd),
    /// Update function configurations.
    Update(UpdateCmd),
    /// Delete function.
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
    /// Function name.
    #[arg(long)]
    pub name: String,
    /// Output format.
    #[clap(long, value_enum, default_value_t)]
    pub format: OutputFormat,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Function name.
    #[arg(long)]
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
    logging::init(cmd.verbose as usize).expect("no error expected");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = FunctionService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List(cmd) => {
            let response = service.list_functions().await?;
            println_fmt(&response.ids, cmd.format)?;
        }
        ModeCmd::Show(cmd) => {
            let function = service.get_function(&cmd.name).await?;
            println_fmt(&function, cmd.format)?;
        }
        ModeCmd::Update(cmd) => {
            service.update_functions(cmd).await?;
            println!("OK");
        }
        ModeCmd::Delete(cmd) => {
            service.delete_function(cmd).await?;
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

pub struct FunctionService {
    client: FunctionServiceClient<LayeredChannel>,
}

impl FunctionService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = FunctionServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn list_functions(&mut self) -> Result<ListFunctionsResponse, Box<dyn Error>> {
        let response = self.client.list(ListFunctionsRequest {}).await?.into_inner();
        Ok(response)
    }

    pub async fn get_function(&mut self, name: &str) -> Result<Option<Function>, Box<dyn Error>> {
        let request = GetFunctionRequest {
            id: Some(FunctionId { name: name.to_string() }),
        };
        let response = self.client.get(request).await?.into_inner();
        Ok(response.function)
    }

    pub async fn update_functions(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdateFunctionRequest {
            function: Some(Function {
                id: Some(FunctionId { name: cmd.name }),
                chains: cmd.chains,
            }),
        };

        self.client.update(request).await?;
        Ok(())
    }

    pub async fn delete_function(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteFunctionRequest {
            id: Some(FunctionId { name: cmd.name }),
        };
        self.client.delete(request).await?;
        Ok(())
    }
}
