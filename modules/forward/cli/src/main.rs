use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::CompleteEnv;
use code::{
    AddL3ForwardRequest, L2ForwardEnableRequest, L3ForwardEntry, RemoveL3ForwardRequest, ShowConfigRequest,
    ShowConfigResponse, TargetModule, forward_service_client::ForwardServiceClient,
    DeleteModuleRequest,
};
use ipnet::IpNet;
use ptree::TreeBuilder;
use tonic::transport::Channel;
use ync::{logging, instance::InstanceMap};

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("forwardpb");
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
    #[arg(long = "mod", short)]
    pub module_name: String,
    /// Output format.
    #[clap(long, value_enum, default_value_t = OutputFormat::Tree)]
    pub format: OutputFormat,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the module to delete
    #[arg(long = "mod", short)]
    pub module_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct L2ForwardCmd {
    /// The name of the module to operate on.
    #[arg(long = "mod", short)]
    pub module_name: String,
    /// Dataplane instances where the changes should be applied, optional.
    /// 
    /// If no instances specified, the route will be applied to all instances nodes.
    #[arg(long)]
    pub instances: Option<Vec<u32>>,
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
    #[arg(long = "mod", short)]
    pub module_name: String,
    /// Dataplane instances where the changes should be applied, optional.
    /// 
    /// If no instances specified, the route will be applied to all instances nodes.
    #[arg(long)]
    pub instances: Option<Vec<u32>>,
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
    #[arg(long = "mod", short)]
    pub module_name: String,
    /// Dataplane instances where the changes should be applied, optional.
    /// 
    /// If no instances specified, the route will be applied to all instances nodes.
    #[arg(long)]
    pub instances: Option<Vec<u32>>,
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
        let request = ShowConfigRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name.to_owned(),
                instances: InstanceMap::MAX.as_u32(),
            }),
        };
        let response = self.client.show_config(request).await?.into_inner();
        match cmd.format {
            OutputFormat::Json => print_json(&response)?,
            OutputFormat::Tree => print_tree(&response)?,
        }

        Ok(())
    }

    pub async fn delete_module(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteModuleRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                instances: InstanceMap::MAX.as_u32(),
            })
        };
        self.client.delete_module(request).await?;
        Ok(())
    }

    pub async fn enable_l2_forward(&mut self, cmd: L2ForwardCmd) -> Result<(), Box<dyn Error>> {
        let request = L2ForwardEnableRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                instances: cmd.instances.map(InstanceMap::from).unwrap_or(InstanceMap::MAX).as_u32(),
            }),
            src_dev_id: cmd.src as u32,
            dst_dev_id: cmd.dst as u32,
        };
        log::trace!("L2ForwardEnableRequest: {:?}", request);
        let response = self.client.enable_l2_forward(request).await?.into_inner();
        log::debug!("L2ForwardEnableResponse: {:?}", response);
        Ok(())
    }

    pub async fn add_l3_forward(&mut self, cmd: AddL3ForwardCmd) -> Result<(), Box<dyn Error>> {
        let request = AddL3ForwardRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                instances: cmd.instances.map(InstanceMap::from).unwrap_or(InstanceMap::MAX).as_u32(),
            }),
            src_dev_id: cmd.src as u32,
            forward: Some(L3ForwardEntry {
                network: cmd.network.to_string(),
                dst_dev_id: cmd.dst as u32,
            }),
        };
        log::trace!("AddL3ForwardRequest: {:?}", request);
        let response = self.client.add_l3_forward(request).await?.into_inner();
        log::debug!("AddL3ForwardResponse: {:?}", response);
        Ok(())
    }

    pub async fn remove_l3_forward(&mut self, cmd: RemoveL3ForwardCmd) -> Result<(), Box<dyn Error>> {
        let request = RemoveL3ForwardRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                instances: cmd.instances.map(InstanceMap::from).unwrap_or(InstanceMap::MAX).as_u32(),
            }),
            src_dev_id: cmd.src as u32,
            network: cmd.network.to_string(),
        };
        log::trace!("RemoveL3ForwardRequest: {:?}", request);
        let response = self.client.remove_l3_forward(request).await?.into_inner();
        log::debug!("RemoveL3ForwardResponse: {:?}", response);
        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = ForwardService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_module(cmd).await,
        ModeCmd::L2Enable(cmd) => service.enable_l2_forward(cmd).await,
        ModeCmd::L3Add(cmd) => service.add_l3_forward(cmd).await,
        ModeCmd::L3Remove(cmd) => service.remove_l3_forward(cmd).await,
    }
}

pub fn print_json(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    println!("{}", serde_json::to_string(resp)?);
    Ok(())
}

pub fn print_tree(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Forward Configs".to_string());

    for instance in &resp.configs {
        tree.begin_child(format!("'{}' on instance {}", resp.name, instance.instance));

        for dev in instance.devices.iter() {
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
