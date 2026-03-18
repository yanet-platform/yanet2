//! CLI for YANET "function" module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::FunctionId;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};
use ynpb::pb::{
    function_service_client::FunctionServiceClient, DeleteFunctionRequest, Function, FunctionChain,
    UpdateFunctionRequest,
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
    /// Update function configurations.
    Update(UpdateCmd),
    /// Delete function.
    Delete(DeleteCmd),
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
        ModeCmd::Update(cmd) => service.update_functions(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_function(cmd).await,
    }
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

    pub async fn update_functions(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdateFunctionRequest {
            function: Some(Function {
                id: Some(FunctionId { name: cmd.name }),
                chains: cmd.chains,
            }),
        };

        self.client.update(request).await?;
        log::info!("Successfully updated functions");
        Ok(())
    }

    pub async fn delete_function(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteFunctionRequest {
            id: Some(FunctionId { name: cmd.name }),
        };
        self.client.delete(request).await?;
        log::info!("Successfully deleted function");
        Ok(())
    }
}
