//! CLI for YANET "function" module.

use core::error::Error;
use std::str::FromStr;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{
    function_service_client::FunctionServiceClient, Chain, DeleteFunctionRequest, Function, FunctionChain,
    UpdateFunctionRequest,
};
use commonpb::pb::{FunctionId, ModuleId};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};

#[allow(non_snake_case)]
pub mod code {
    tonic::include_proto!("ynpb");
}

impl FromStr for FunctionChain {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let (chain_part, modules_part) = s
            .split_once('=')
            .ok_or_else(|| format!("invalid chain format: expected 'name:weight=modules', got {s:?}"))?;
        let (name, weight) = chain_part
            .split_once(':')
            .ok_or_else(|| format!("invalid chain format: expected 'name:weight', got {chain_part:?}"))?;
        let weight = weight
            .parse::<u64>()
            .map_err(|e| format!("invalid chain weight {weight:?}: {e}"))?;
        let modules = modules_part
            .split(',')
            .map(|m| -> Result<ModuleId, Box<dyn Error>> {
                let (r#type, name) = m
                    .split_once(':')
                    .ok_or_else(|| format!("invalid module format: expected 'module_type:config_name', got {m:?}"))?;
                Ok(ModuleId {
                    r#type: r#type.to_string(),
                    name: name.to_string(),
                })
            })
            .collect::<Result<Vec<_>, _>>()?;
        Ok(FunctionChain {
            weight,
            chain: Some(Chain { name: name.to_string(), modules }),
        })
    }
}

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
    pub chains: Vec<String>,
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
        let chains = cmd
            .chains
            .iter()
            .map(|s| s.parse::<FunctionChain>())
            .collect::<Result<Vec<_>, _>>()?;
        let request = UpdateFunctionRequest {
            function: Some(Function {
                id: Some(FunctionId { name: cmd.name }),
                chains,
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
