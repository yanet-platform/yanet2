use core::error::Error;
use std::net::{Ipv4Addr, Ipv6Addr};

use clap::{ArgAction, CommandFactory, Parser, Subcommand, ValueEnum};
use clap_complete::CompleteEnv;
use code::{
    nat64_service_client::Nat64ServiceClient, AddMappingRequest, AddPrefixRequest, ListConfigsRequest,
    SetDropUnknownRequest, SetMtuRequest, ShowConfigRequest, ShowConfigResponse,
};
use commonpb::TargetModule;
use ipnet::Ipv6Net;
use ptree::TreeBuilder;
use tonic::transport::Channel;
use yanet_cli::logging;

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;
    tonic::include_proto!("nat64pb");
}

#[allow(non_snake_case)]
pub mod commonpb {
    use serde::Serialize;
    tonic::include_proto!("commonpb");
}

/// NAT64 module CLI.
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

#[derive(Debug, Clone, Subcommand)]
pub enum ModeCmd {
    /// List all NAT64 configurations
    List,
    /// Show current configuration
    Show(ShowConfigCmd),
    /// Manage NAT64 prefixes
    Prefix {
        #[clap(subcommand)]
        cmd: PrefixCmd,
    },
    /// Manage NAT64 mappings
    Mapping {
        #[clap(subcommand)]
        cmd: MappingCmd,
    },
    /// Set MTU values
    Mtu(MtuCmd),
    /// Set drop_unknown flags
    Drop(DropCmd),
}

#[derive(Debug, Clone, Subcommand)]
pub enum PrefixCmd {
    /// Add a new NAT64 prefix
    Add(AddPrefixCmd),
}

#[derive(Debug, Clone, Subcommand)]
pub enum MappingCmd {
    /// Add a new IPv4-IPv6 mapping
    Add(AddMappingCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// The name of the config to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Output format.
    #[clap(long, value_enum, default_value_t = OutputFormat::Tree)]
    pub format: OutputFormat,
}

#[derive(Debug, Clone, Parser)]
pub struct AddPrefixCmd {
    /// The name of the config to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// IPv6 prefix (12 bytes) to be added.
    #[arg(long)]
    pub prefix: Ipv6Net,
}

#[derive(Debug, Clone, Parser)]
pub struct AddMappingCmd {
    /// The name of the config to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// IPv4 address (4 bytes).
    #[arg(long)]
    pub ipv4: Ipv4Addr,
    /// IPv6 address (16 bytes).
    #[arg(long)]
    pub ipv6: Ipv6Addr,
    /// Index of the prefix to use.
    #[arg(long)]
    pub prefix_index: u32,
}

#[derive(Debug, Clone, Parser)]
pub struct MtuCmd {
    /// The name of the config to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// MTU value for IPv4.
    #[arg(long)]
    pub ipv4_mtu: u32,
    /// MTU value for IPv6.
    #[arg(long)]
    pub ipv6_mtu: u32,
}

/// Command for setting drop_unknown flags
#[derive(Debug, Clone, Parser)]
pub struct DropCmd {
    /// The name of the config to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Drop packets with unknown prefix
    #[arg(long)]
    pub drop_unknown_prefix: bool,
    /// Drop packets with unknown mapping
    #[arg(long)]
    pub drop_unknown_mapping: bool,
}

/// Output format options.
#[derive(Debug, Clone, ValueEnum)]
pub enum OutputFormat {
    /// Tree structure with colored output (default).
    Tree,
    /// JSON format.
    Json,
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

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = NAT64Service::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Prefix { cmd } => match cmd {
            PrefixCmd::Add(cmd) => service.add_prefix(cmd).await,
        },
        ModeCmd::Mapping { cmd } => match cmd {
            MappingCmd::Add(cmd) => service.add_mapping(cmd).await,
        },
        ModeCmd::Mtu(cmd) => service.set_mtu(cmd).await,
        ModeCmd::Drop(cmd) => service.set_drop_unknown(cmd).await,
    }
}

pub struct NAT64Service {
    client: Nat64ServiceClient<Channel>,
}

impl NAT64Service {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = Nat64ServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    pub async fn list_configs(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");
        let response = self.client.list_configs(request).await?.into_inner();
        log::debug!("list configs response: {response:?}");

        let mut tree = TreeBuilder::new("List NAT64 Configs".to_string());
        for config in response.configs {
            tree.add_empty_child(config);
        }
        let tree = tree.build();
        ptree::print_tree(&tree)?;
        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowConfigRequest {
            target: Some(TargetModule {
                config_name: cmd.config_name.to_owned(),
            }),
        };
        log::trace!("show config request: {request:?}");
        let response = self.client.show_config(request).await?.into_inner();
        log::debug!("show config response: {response:?}");

        match cmd.format {
            OutputFormat::Json => print_json(&response)?,
            OutputFormat::Tree => print_tree(&response)?,
        }
        Ok(())
    }

    pub async fn add_prefix(&mut self, cmd: AddPrefixCmd) -> Result<(), Box<dyn Error>> {
        let request = AddPrefixRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
            prefix: cmd.prefix.addr().octets()[..12].to_vec(),
        };
        log::debug!("AddPrefixRequest: {request:?}");
        let response = self.client.add_prefix(request).await?.into_inner();
        log::debug!("AddPrefixResponse: {response:?}");
        Ok(())
    }

    pub async fn add_mapping(&mut self, cmd: AddMappingCmd) -> Result<(), Box<dyn Error>> {
        let request = AddMappingRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
            ipv4: cmd.ipv4.octets().to_vec(),
            ipv6: cmd.ipv6.octets().to_vec(),
            prefix_index: cmd.prefix_index,
        };
        log::debug!("AddMappingRequest: {request:?}");
        let response = self.client.add_mapping(request).await?.into_inner();
        log::debug!("AddMappingResponse: {response:?}");
        Ok(())
    }

    pub async fn set_mtu(&mut self, cmd: MtuCmd) -> Result<(), Box<dyn Error>> {
        let request = SetMtuRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
            mtu: Some(code::MtuConfig {
                ipv4_mtu: cmd.ipv4_mtu,
                ipv6_mtu: cmd.ipv6_mtu,
            }),
        };
        log::debug!("SetMtuRequest: {request:?}");
        let response = self.client.set_mtu(request).await?.into_inner();
        log::debug!("SetMtuResponse: {response:?}");
        Ok(())
    }

    pub async fn set_drop_unknown(&mut self, cmd: DropCmd) -> Result<(), Box<dyn Error>> {
        let request = SetDropUnknownRequest {
            target: Some(TargetModule { config_name: cmd.config_name.clone() }),
            drop_unknown_prefix: cmd.drop_unknown_prefix,
            drop_unknown_mapping: cmd.drop_unknown_mapping,
        };
        log::debug!("SetDropUnknownRequest: {request:?}");
        let response = self.client.set_drop_unknown(request).await?.into_inner();
        log::debug!("SetDropUnknownResponse: {response:?}");
        Ok(())
    }
}

pub fn print_json(config: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    println!("{}", serde_json::to_string(config)?);
    Ok(())
}

pub fn print_tree(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("NAT64 Config".to_string());

    if let Some(config) = &resp.config {
        tree.begin_child("Prefixes".to_string());
        for (idx, prefix) in config.prefixes.iter().enumerate() {
            tree.add_empty_child(format!("{}: {:?}", idx, prefix.prefix));
        }
        tree.end_child();

        tree.begin_child("Mappings".to_string());
        for mapping in &config.mappings {
            tree.add_empty_child(format!(
                "IPv4: {:?} -> IPv6: {:?} (prefix: {})",
                mapping.ipv4, mapping.ipv6, mapping.prefix_index
            ));
        }
        tree.end_child();

        if let Some(mtu) = &config.mtu {
            tree.begin_child("MTU".to_string());
            tree.add_empty_child(format!("IPv4: {}", mtu.ipv4_mtu));
            tree.add_empty_child(format!("IPv6: {}", mtu.ipv6_mtu));
            tree.end_child();
        }

        tree.add_empty_child(format!("DropUnknownPrefix: {}", config.drop_unknown_prefix));
        tree.add_empty_child(format!("DropUnknownMapping: {}", config.drop_unknown_mapping));
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;

    Ok(())
}
