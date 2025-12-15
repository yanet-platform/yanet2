//! CLI for YANET "counters" module.

use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{
    counters_service_client::CountersServiceClient, ChainCountersRequest, DeviceCountersRequest,
    FunctionCountersRequest, ModuleCountersRequest, PipelineCountersRequest,
};
use tonic::transport::Channel;
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("ynpb");
}

/// Counters module - displays counters information.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Output format.
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Show device counters.
    Device(DeviceCmd),
    /// Show pipeline counters.
    Pipeline(PipelineCmd),
    /// Show pipeline counters.
    Function(FunctionCmd),
    /// Show pipeline counters.
    Chain(ChainCmd),
    /// Show counters of module assigned to a pipeline.
    Module(ModuleCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct DeviceCmd {
    #[arg(long)]
    pub instance: u32,
    #[arg(long)]
    pub device_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct PipelineCmd {
    #[arg(long)]
    pub instance: u32,
    #[arg(long)]
    pub device_name: String,
    #[arg(long)]
    pub pipeline_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct FunctionCmd {
    #[arg(long)]
    pub instance: u32,
    #[arg(long)]
    pub device_name: String,
    #[arg(long)]
    pub pipeline_name: String,
    #[arg(long)]
    pub function_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ChainCmd {
    #[arg(long)]
    pub instance: u32,
    #[arg(long)]
    pub device_name: String,
    #[arg(long)]
    pub pipeline_name: String,
    #[arg(long)]
    pub function_name: String,
    #[arg(long)]
    pub chain_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ModuleCmd {
    #[arg(long)]
    pub instance: u32,
    #[arg(long)]
    pub device_name: String,
    #[arg(long)]
    pub pipeline_name: String,
    #[arg(long)]
    pub function_name: String,
    #[arg(long)]
    pub chain_name: String,
    #[arg(long)]
    pub module_type: String,
    #[arg(long)]
    pub module_name: String,
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
    let mut service = CountersService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Device(cmd) => service.show_device(cmd.instance, cmd.device_name).await?,
        ModeCmd::Pipeline(cmd) => {
            service
                .show_pipeline(cmd.instance, cmd.device_name, cmd.pipeline_name)
                .await?
        }
        ModeCmd::Function(cmd) => {
            service
                .show_function(cmd.instance, cmd.device_name, cmd.pipeline_name, cmd.function_name)
                .await?
        }
        ModeCmd::Chain(cmd) => {
            service
                .show_chain(
                    cmd.instance,
                    cmd.device_name,
                    cmd.pipeline_name,
                    cmd.function_name,
                    cmd.chain_name,
                )
                .await?
        }
        ModeCmd::Module(cmd) => {
            service
                .show_module(
                    cmd.instance,
                    cmd.device_name,
                    cmd.pipeline_name,
                    cmd.function_name,
                    cmd.chain_name,
                    cmd.module_type,
                    cmd.module_name,
                )
                .await?
        }
    }

    Ok(())
}

pub struct CountersService {
    client: CountersServiceClient<Channel>,
}

impl CountersService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = CountersServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    pub async fn show_device(&mut self, instance: u32, device_name: String) -> Result<(), Box<dyn Error>> {
        let request = DeviceCountersRequest {
            dp_instance: instance,
            device: device_name,
        };
        let response = self.client.device(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }

    pub async fn show_pipeline(
        &mut self,
        instance: u32,
        device_name: String,
        pipeline_name: String,
    ) -> Result<(), Box<dyn Error>> {
        let request = PipelineCountersRequest {
            dp_instance: instance,
            device: device_name,
            pipeline: pipeline_name,
        };
        let response = self.client.pipeline(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }

    pub async fn show_function(
        &mut self,
        instance: u32,
        device_name: String,
        pipeline_name: String,
        function_name: String,
    ) -> Result<(), Box<dyn Error>> {
        let request = FunctionCountersRequest {
            dp_instance: instance,
            device: device_name,
            pipeline: pipeline_name,
            function: function_name,
        };
        let response = self.client.function(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }

    pub async fn show_chain(
        &mut self,
        instance: u32,
        device_name: String,
        pipeline_name: String,
        function_name: String,
        chain_name: String,
    ) -> Result<(), Box<dyn Error>> {
        let request = ChainCountersRequest {
            dp_instance: instance,
            device: device_name,
            pipeline: pipeline_name,
            function: function_name,
            chain: chain_name,
        };
        let response = self.client.chain(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }

    pub async fn show_module(
        &mut self,
        instance: u32,
        device_name: String,
        pipeline_name: String,
        function_name: String,
        chain_name: String,
        module_type: String,
        module_name: String,
    ) -> Result<(), Box<dyn Error>> {
        let request = ModuleCountersRequest {
            dp_instance: instance,
            device: device_name,
            pipeline: pipeline_name,
            function: function_name,
            chain: chain_name,
            module_type,
            module_name,
        };
        let response = self.client.module(request).await?;
        println!("{}", serde_json::to_string(response.get_ref())?);
        Ok(())
    }
}
