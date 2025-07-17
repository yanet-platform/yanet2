use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::CompleteEnv;
use code::{
    AddL3ForwardRequest, DeleteConfigRequest, L2ForwardEnableRequest, L3ForwardEntry, RemoveL3ForwardRequest,
    ShowConfigRequest, ShowConfigResponse, forward_service_client::ForwardServiceClient,
};
use commonpb::TargetModule;
use ipnet::IpNet;
use ptree::TreeBuilder;
use tonic::transport::Channel;
use ync::logging;

use crate::code::ListConfigsRequest;

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("forwardpb");
}

#[allow(non_snake_case)]
pub mod commonpb {
    use serde::Serialize;

    tonic::include_proto!("commonpb");
}

/// Forward module.
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
    L2Enable(L2ForwardCmd),
    L3Add(AddL3ForwardCmd),
    L3Remove(RemoveL3ForwardCmd),
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
pub struct L2ForwardCmd {
    /// The name of the module to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Dataplane instances where the changes should be applied.
    #[arg(long, short, required = true)]
    pub instances: Vec<u32>,
    /// Source device ID.
    #[arg(required = true, long = "src", value_name = "src-dev-id")]
    pub src: u16,
    /// Destination device ID.
    #[arg(required = true, long = "dst", value_name = "dst-dev-id")]
    pub dst: u16,
}

#[derive(Debug, Clone, Parser)]
pub struct AddL3ForwardCmd {
    /// The name of the module to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Dataplane instances where the changes should be applied.
    #[arg(long, short, required = true)]
    pub instances: Vec<u32>,
    /// Source device ID.
    #[arg(required = true, long = "src", value_name = "src-dev-id")]
    pub src: u16,
    /// Network prefix.
    #[arg(required = true, long = "net", value_name = "network")]
    pub network: IpNet,
    /// Destination device ID.
    #[arg(required = true, long = "dst", value_name = "dst-dev-id")]
    pub dst: u16,
}

#[derive(Debug, Clone, Parser)]
pub struct RemoveL3ForwardCmd {
    /// The name of the module to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Dataplane instances where the changes should be applied.
    #[arg(long, short, required = true)]
    pub instances: Vec<u32>,
    /// Source device ID.
    #[arg(required = true, long = "src", value_name = "src-dev-id")]
    pub src: u16,
    /// Network prefix.
    #[arg(required = true, long = "net", value_name = "network")]
    pub network: IpNet,
}

pub struct ForwardService {
    client: ForwardServiceClient<Channel>,
}

impl ForwardService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = ForwardServiceClient::connect(endpoint).await?;
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

    pub async fn enable_l2_forward(&mut self, cmd: L2ForwardCmd) -> Result<(), Box<dyn Error>> {
        for instance in cmd.instances {
            let request = L2ForwardEnableRequest {
                target: Some(TargetModule {
                    config_name: cmd.config_name.clone(),
                    dataplane_instance: instance,
                }),
                src_dev_id: cmd.src as u32,
                dst_dev_id: cmd.dst as u32,
            };
            log::trace!("L2ForwardEnableRequest: {request:?}");
            let response = self.client.enable_l2_forward(request).await?.into_inner();
            log::debug!("L2ForwardEnableResponse: {response:?}");
        }
        Ok(())
    }

    pub async fn add_l3_forward(&mut self, cmd: AddL3ForwardCmd) -> Result<(), Box<dyn Error>> {
        for instance in cmd.instances {
            let request = AddL3ForwardRequest {
                target: Some(TargetModule {
                    config_name: cmd.config_name.clone(),
                    dataplane_instance: instance,
                }),
                src_dev_id: cmd.src as u32,
                forward: Some(L3ForwardEntry {
                    network: cmd.network.to_string(),
                    dst_dev_id: cmd.dst as u32,
                }),
            };
            log::trace!("AddL3ForwardRequest: {request:?}");
            let response = self.client.add_l3_forward(request).await?.into_inner();
            log::debug!("AddL3ForwardResponse: {response:?}");
        }
        Ok(())
    }

    pub async fn remove_l3_forward(&mut self, cmd: RemoveL3ForwardCmd) -> Result<(), Box<dyn Error>> {
        for instance in cmd.instances {
            let request = RemoveL3ForwardRequest {
                target: Some(TargetModule {
                    config_name: cmd.config_name.clone(),
                    dataplane_instance: instance,
                }),
                src_dev_id: cmd.src as u32,
                network: cmd.network.to_string(),
            };
            log::trace!("RemoveL3ForwardRequest: {request:?}");
            let response = self.client.remove_l3_forward(request).await?.into_inner();
            log::debug!("RemoveL3ForwardResponse: {response:?}");
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
        let mut tree = TreeBuilder::new("List Forward Configs".to_string());
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
    let mut service = ForwardService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::L2Enable(cmd) => service.enable_l2_forward(cmd).await,
        ModeCmd::L3Add(cmd) => service.add_l3_forward(cmd).await,
        ModeCmd::L3Remove(cmd) => service.remove_l3_forward(cmd).await,
    }
}

pub fn print_json(configs: Vec<ShowConfigResponse>) -> Result<(), Box<dyn Error>> {
    println!("{}", serde_json::to_string(&configs)?);
    Ok(())
}

pub fn print_tree(configs: Vec<ShowConfigResponse>) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("View Forward Configs".to_string());

    for config in &configs {
        tree.begin_child(format!("Instance {}", config.instance));

        if let Some(config) = &config.config {
            for dev in &config.devices {
                tree.begin_child(format!("dev-id {}", dev.src_dev_id));
                tree.add_empty_child(format!("L2 via dev-id {}", dev.dst_dev_id));
                if !dev.forwards.is_empty() {
                    tree.begin_child(format!("L3 forwards (num={})", dev.forwards.len()));
                    for fwd in &dev.forwards {
                        tree.add_empty_child(format!("{} via dev-id {}", fwd.network, fwd.dst_dev_id));
                    }
                    tree.end_child();
                }
                tree.end_child();
            }
        }

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
