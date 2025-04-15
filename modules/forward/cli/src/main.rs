use core::error::Error;

use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::CompleteEnv;
use code::{
    AddDeviceRequest, AddForwardRequest, ForwardEntry, RemoveDeviceRequest, RemoveForwardRequest, ShowConfigRequest,
    ShowConfigResponse, TargetModule, forward_service_client::ForwardServiceClient,
};
use ipnet::IpNet;
use ptree::TreeBuilder;
use tonic::transport::Channel;

use ync::logging;

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
    DeviceAdd(AddDeviceCmd),
    DeviceRemove(RemoveDeviceCmd),
    ForwardAdd(AddForwardCmd),
    ForwardRemove(RemoveForwardCmd),
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
pub struct AddDeviceCmd {
    /// The name of the module to operate on.
    #[arg(long = "mod", short)]
    pub module_name: String,
    /// NUMA node index where the changes should be applied, optional.
    ///
    /// If no numa specified, the route will be applied to all NUMA nodes.
    #[arg(long)]
    pub numa: Option<Vec<u32>>,

    /// The DeviceID to be added to the forward module configuration.
    #[arg(required = true, long = "dev-id", short)]
    pub device: u16,
}

#[derive(Debug, Clone, Parser)]
pub struct RemoveDeviceCmd {
    /// The name of the module to operate on.
    #[arg(long = "mod", short)]
    pub module_name: String,
    /// NUMA node index where the changes should be applied, optional.
    ///
    /// If no numa specified, the route will be applied to all NUMA nodes.
    #[arg(long)]
    pub numa: Option<Vec<u32>>,

    /// The DeviceID to be removed from the forward module configuration.
    ///
    /// Removing a device results in removing all forwarding rules
    /// that point to the removed device.
    #[arg(required = true, long = "dev-id", short)]
    pub device: u16,
}

#[derive(Debug, Clone, Parser)]
pub struct AddForwardCmd {
    /// The name of the module to operate on.
    #[arg(long = "mod", short)]
    pub module_name: String,
    /// NUMA node index where the changes should be applied, optional.
    ///
    /// If no numa specified, the route will be applied to all NUMA nodes.
    #[arg(long)]
    pub numa: Option<Vec<u32>>,

    /// The deviceId where the forwarding rule will be added.
    #[arg(required = true, num_args(1), value_name = "dev-id")]
    pub device: u16,
    /// The network to which the forwarding rule will be created.
    #[arg(required = true, num_args(1), value_name = "network")]
    pub network: IpNet,
    /// The target device ID where the forwarding rule points.
    #[arg(required = true, num_args(1), value_name = "via-id")]
    pub target: u16,
}

#[derive(Debug, Clone, Parser)]
pub struct RemoveForwardCmd {
    /// The name of the module to operate on.
    #[arg(long = "mod", short)]
    pub module_name: String,
    /// NUMA node index where the changes should be applied, optional.
    ///
    /// If no numa specified, the route will be applied to all NUMA nodes.
    #[arg(long)]
    pub numa: Option<Vec<u32>>,

    /// The device ID from which the forwarding rule will be removed.
    #[arg(required = true, num_args(1), value_name = "dev-id")]
    pub device: u16,
    /// The network from which the forwarding rule will be removed.
    #[arg(required = true, num_args(1), value_name = "network")]
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
                numa: Vec::new(),
            }),
        };
        let response = self.client.show_config(request).await?.into_inner();
        match cmd.format {
            OutputFormat::Json => print_json(&response)?,
            OutputFormat::Tree => print_tree(&response)?,
        }

        Ok(())
    }

    pub async fn add_device(&mut self, cmd: AddDeviceCmd) -> Result<(), Box<dyn Error>> {
        let request = AddDeviceRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                numa: cmd.numa.unwrap_or_default(),
            }),
            device_id: cmd.device as u32,
        };
        log::trace!("AddDeviceRequest: {:?}", request);
        let response = self.client.add_device(request).await?.into_inner();
        log::debug!("AddDeviceResponse: {:?}", response);
        Ok(())
    }

    pub async fn remove_device(&mut self, cmd: RemoveDeviceCmd) -> Result<(), Box<dyn Error>> {
        let request = RemoveDeviceRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                numa: cmd.numa.unwrap_or_default(),
            }),
            device_id: cmd.device as u32,
        };
        log::trace!("RemoveDeviceRequest: {:?}", request);
        let response = self.client.remove_device(request).await?.into_inner();
        log::debug!("RemoveDeviceResponse: {:?}", response);
        Ok(())
    }

    pub async fn add_forward(&mut self, cmd: AddForwardCmd) -> Result<(), Box<dyn Error>> {
        let request = AddForwardRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                numa: cmd.numa.unwrap_or_default(),
            }),
            device_id: cmd.device as u32,
            forward: Some(ForwardEntry {
                network: cmd.network.to_string(),
                device_id: cmd.target as u32,
            }),
        };
        log::trace!("AddForwardRequest: {:?}", request);
        let response = self.client.add_forward(request).await?.into_inner();
        log::debug!("AddForwardResponse: {:?}", response);
        Ok(())
    }

    pub async fn remove_forward(&mut self, cmd: RemoveForwardCmd) -> Result<(), Box<dyn Error>> {
        let request = RemoveForwardRequest {
            target: Some(TargetModule {
                module_name: cmd.module_name,
                numa: cmd.numa.unwrap_or_default(),
            }),
            device_id: cmd.device as u32,
            network: cmd.network.to_string(),
        };
        log::trace!("RemoveForwardRequest: {:?}", request);
        let response = self.client.remove_forward(request).await?.into_inner();
        log::debug!("RemoveForwardResponse: {:?}", response);
        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = ForwardService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::DeviceAdd(cmd) => service.add_device(cmd).await,
        ModeCmd::DeviceRemove(cmd) => service.remove_device(cmd).await,
        ModeCmd::ForwardAdd(cmd) => service.add_forward(cmd).await,
        ModeCmd::ForwardRemove(cmd) => service.remove_forward(cmd).await,
    }
}

pub fn print_json(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    println!("{}", serde_json::to_string(resp)?);
    Ok(())
}

pub fn print_tree(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Forward Configs".to_string());

    for instance in &resp.configs {
        tree.begin_child(format!("NUMA {}", instance.numa));

        tree.begin_child("Forwards".to_string());
        for dev in instance.devices.iter() {
            tree.begin_child(format!("DeviceId: {}", dev.device_id));
            for fwd in &dev.forwards {
                tree.add_empty_child(format!("{} via dev-id {}", fwd.network, fwd.device_id));
            }
            tree.end_child();
        }

        tree.end_child();

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
