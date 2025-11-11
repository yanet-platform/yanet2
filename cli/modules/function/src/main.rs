//! CLI for YANET "function" module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{
    function_service_client::FunctionServiceClient, Chain, ChainModule, DeleteFunctionRequest, Function, FunctionChain,
    UpdateFunctionsRequest,
};
use tonic::transport::Channel;
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    tonic::include_proto!("ynpb");
}

/// Function module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
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
    /// Dataplane instance where the changes should be applied.
    #[arg(long)]
    pub instance: u32,
    /// Function name.
    #[arg(long)]
    pub name: String,
    /// Chains in format name:weight=type:name,type:name
    #[arg(long)]
    pub chains: Vec<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// Dataplane instance where the changes should be applied.
    #[arg(short, long)]
    pub instance: u32,
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
    let mut service = FunctionService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Update(cmd) => service.update_functions(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_function(cmd).await,
    }
}

pub struct FunctionService {
    client: FunctionServiceClient<Channel>,
}

impl FunctionService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let channel = Channel::from_shared(endpoint)?.connect().await?;
        let client = FunctionServiceClient::new(channel);
        Ok(Self { client })
    }

    pub async fn update_functions(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        let request = UpdateFunctionsRequest {
            instance: cmd.instance,
            functions: vec![Function {
                name: cmd.name,
                chains: cmd
                    .chains
                    .into_iter()
                    .map(|m| {
                        let chain_modules: Vec<&str> = m.split('=').collect();
                        if chain_modules.len() != 2 {
                            panic!("Invalid chain format");
                        }
                        let chain_weight: Vec<&str> = chain_modules[0].split(':').collect();
                        if chain_weight.len() != 2 {
                            panic!("Invalid chain format");
                        }
                        let modules: Vec<&str> = chain_modules[1].split(',').collect();
                        FunctionChain {
                            weight: chain_weight[1].parse::<u64>().expect("invalid chain format"),
                            chain: Some(Chain {
                                name: chain_weight[0].to_string(),
                                modules: modules
                                    .into_iter()
                                    .map(|m| {
                                        let parts: Vec<&str> = m.split(':').collect();
                                        if parts.len() != 2 {
                                            panic!("Invalid module format. Expected 'module_name:config_name'");
                                        }
                                        ChainModule {
                                            r#type: parts[0].to_string(),
                                            name: parts[1].to_string(),
                                        }
                                    })
                                    .collect(),
                            }),
                        }
                    })
                    .collect(),
            }],
        };

        self.client.update(request).await?;
        log::info!("Successfully updated functions");
        Ok(())
    }

    pub async fn delete_function(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteFunctionRequest {
            instance: cmd.instance,
            function_name: cmd.name,
        };
        self.client.delete(request).await?;
        log::info!("Successfully deleted function");
        Ok(())
    }
}
