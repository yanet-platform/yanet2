use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum, Subcommand};
use clap_complete::CompleteEnv;
use code::{
    DeleteConfigRequest, ShowConfigRequest, ShowConfigResponse, proxy_service_client::ProxyServiceClient,
    SetAddrRequest,
};
use commonpb::TargetModule;
use ptree::TreeBuilder;
use tonic::transport::Channel;
use ync::logging;

use crate::code::ListConfigsRequest;

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("proxypb");
}

#[allow(non_snake_case)]
pub mod commonpb {
    use serde::Serialize;

    tonic::include_proto!("commonpb");
}

/// Proxy module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

/// Output format options.
#[derive(Debug, Clone, ValueEnum)]
pub enum OutputFormat {
    /// Tree structure with colored output (default).
    Tree,
    /// JSON format.
    Json,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    Show(ShowConfigCmd),
    Delete(DeleteCmd),

    Addr {
        #[clap(subcommand)]
        cmd: AddrCmd,
    }
}

#[derive(Debug, Clone, Subcommand)]
pub enum AddrCmd {
    Set(SetAddrCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// The name of the module to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: Option<String>,
    /// Indices of dataplane instances from which configurations should be retrieved.
    #[arg(long, short, required = false)]
    pub instances: Vec<u32>,
    /// Output format.
    #[clap(long, value_enum, default_value_t = OutputFormat::Tree)]
    pub format: OutputFormat,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the module to delete
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Dataplane instances from which to delete config
    #[arg(long, short, required = true)]
    pub instances: Vec<u32>,
}

#[derive(Debug, Clone, Parser)]
pub struct SetAddrCmd {
    #[arg(long = "cfg", short)]
    pub config_name: String,
    #[arg(long, short, required = false)]
    pub instances: Vec<u32>,
    #[arg(long)]
    pub addr: u32,
}

pub struct ProxyService {
    client: ProxyServiceClient<Channel>,
}

impl ProxyService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = ProxyServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Box<dyn Error>> {
        let Some(name) = cmd.config_name else {
            self.print_config_list().await?;
            return Ok(());
        };

        let mut instances = cmd.instances;
        if instances.is_empty() {
            instances = self.get_dataplane_instances().await?;
        }
        let mut configs = Vec::new();
        for instance in instances {
            let request = ShowConfigRequest {
                target: Some(TargetModule {
                    config_name: name.to_owned(),
                    dataplane_instance: instance,
                }),
            };
            log::trace!("show config request on dataplane instance {instance}: {request:?}");
            let response = self.client.show_config(request).await?.into_inner();
            log::debug!("show config response on dataplane instance {instance}: {response:?}");
            configs.push(response);
        }

        match cmd.format {
            OutputFormat::Json => print_json(configs)?,
            OutputFormat::Tree => print_tree(configs)?,
        }

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        for instance in cmd.instances {
            let request = DeleteConfigRequest {
                target: Some(TargetModule {
                    config_name: cmd.config_name.clone(),
                    dataplane_instance: instance,
                }),
            };
            self.client.delete_config(request).await?;
        }
        Ok(())
    }

    pub async fn set_addr(&mut self, cmd: SetAddrCmd) -> Result<(), Box<dyn Error>> {
        for instance in cmd.instances {
            let request = SetAddrRequest {
                target: Some(TargetModule {
                    config_name: cmd.config_name.clone(),
                    dataplane_instance: instance,
                }),
                addr: cmd.addr,
            };
            log::debug!("SetAddrRequest: {request:?}");
            let response = self.client.set_addr(request).await?;
            log::debug!("SetAddrResponse: {response:?}");
        }
        Ok(())
    }

    async fn get_dataplane_instances(&mut self) -> Result<Vec<u32>, Box<dyn Error>> {
        let request = ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();
        Ok(response.instance_configs.iter().map(|c| c.instance).collect())
    }

    async fn print_config_list(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();
        let mut tree = TreeBuilder::new("List Proxy Configs".to_string());
        for instance_config in response.instance_configs {
            tree.begin_child(format!("Instance {}", instance_config.instance));
            for config in instance_config.configs {
                tree.add_empty_child(config);
            }
        }
        let tree = tree.build();
        ptree::print_tree(&tree)?;
        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = ProxyService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Addr { cmd } => match cmd {
            AddrCmd::Set(cmd) => service.set_addr(cmd).await,
        },
    }
}

pub fn print_json(configs: Vec<ShowConfigResponse>) -> Result<(), Box<dyn Error>> {
    println!("{}", serde_json::to_string(&configs)?);
    Ok(())
}

pub fn print_tree(configs: Vec<ShowConfigResponse>) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("View Proxy Configs".to_string());

    for config in &configs {
        tree.begin_child(format!("Instance {}", config.instance));

        // if let Some(config) = &config.config {
        // }

        tree.end_child();
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;

    Ok(())
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("initialize logging");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}
