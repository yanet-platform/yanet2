use core::net::{Ipv4Addr, Ipv6Addr};

use clap::{ArgAction, CommandFactory, Parser, Subcommand};
use clap_complete::CompleteEnv;
use commonpb::pb::IpAddress;
use nat64pb::{
    nat64_service_client::Nat64ServiceClient, AddMappingRequest, AddPrefixRequest, ListConfigsRequest,
    RemoveMappingRequest, RemovePrefixRequest, SetDropUnknownRequest, SetMtuRequest, ShowConfigRequest,
    ShowConfigResponse,
};
use netip::{Contiguous, Ipv6Network};
use ptree::TreeBuilder;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::{Error, NotFoundMapper},
    output::{self, CommonFormat},
};

#[allow(non_snake_case)]
pub mod nat64pb {
    use serde::Serialize;
    tonic::include_proto!("modules.nat64.controlplane.nat64pb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.nat64.controlplane.nat64pb.v1.NAT64Service";

/// Maps a genuine "config not found" status into a friendly message.
const NOT_FOUND: NotFoundMapper = NotFoundMapper::new(SERVICE_NAME, "requested config");

/// NAT64 module CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
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
    /// Remove NAT64 prefix
    Remove(RemovePrefixCmd),
}

#[derive(Debug, Clone, Subcommand)]
pub enum MappingCmd {
    /// Add a new IPv4-IPv6 mapping
    Add(AddMappingCmd),
    /// Remove IPv4-IPv6 mapping
    Remove(RemoveMappingCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct AddPrefixCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// IPv6 prefix (12 bytes) to be added.
    #[arg(long)]
    pub prefix: Contiguous<Ipv6Network>,
}

#[derive(Debug, Clone, Parser)]
pub struct RemovePrefixCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// IPv6 prefix (12 bytes) to be removed.
    #[arg(long)]
    pub prefix: Contiguous<Ipv6Network>,
}

#[derive(Debug, Clone, Parser)]
pub struct AddMappingCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n')]
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
pub struct RemoveMappingCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// IPv4 address (4 bytes).
    #[arg(long)]
    pub ipv4: Ipv4Addr,
}

#[derive(Debug, Clone, Parser)]
pub struct MtuCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n')]
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
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// Drop packets with unknown prefix
    #[arg(long)]
    pub drop_unknown_prefix: bool,
    /// Drop packets with unknown mapping
    #[arg(long)]
    pub drop_unknown_mapping: bool,
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = NAT64Service::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Prefix { cmd } => match cmd {
            PrefixCmd::Add(cmd) => service.add_prefix(cmd).await,
            PrefixCmd::Remove(cmd) => service.remove_prefix(cmd).await,
        },
        ModeCmd::Mapping { cmd } => match cmd {
            MappingCmd::Add(cmd) => service.add_mapping(cmd).await,
            MappingCmd::Remove(cmd) => service.remove_mapping(cmd).await,
        },
        ModeCmd::Mtu(cmd) => service.set_mtu(cmd).await,
        ModeCmd::Drop(cmd) => service.set_drop_unknown(cmd).await,
    }
}

pub struct NAT64Service {
    service: Service<Nat64ServiceClient<LayeredChannel>>,
}

impl NAT64Service {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            Nat64ServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");
        let response = self
            .service
            .client()
            .list_configs(request)
            .await
            .map_err(self.service.status("list"))?
            .into_inner();
        log::debug!("list configs response: {response:?}");

        output::data(
            &response.configs,
            response.configs.is_empty(),
            format_args!("no nat64 configs"),
            || {
                let mut tree = TreeBuilder::new("List NAT64 Configs".to_owned());
                for config in &response.configs {
                    tree.add_empty_child(config.clone());
                }
                let _ = ptree::print_tree(&tree.build());
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
        log::trace!("show config request: {request:?}");
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(|status| {
                NOT_FOUND.map(
                    status,
                    "show",
                    self.service.endpoint(),
                    Some(&format!("config '{}'", cmd.config_name)),
                )
            })?
            .into_inner();
        log::debug!("show config response: {response:?}");

        output::data(&response, false, format_args!(""), || print_tree(&response));

        Ok(())
    }

    pub async fn add_prefix(&mut self, cmd: AddPrefixCmd) -> Result<(), Error> {
        let request = AddPrefixRequest {
            name: cmd.config_name.clone(),
            prefix: cmd.prefix.addr().octets()[..12].to_vec(),
        };
        log::debug!("AddPrefixRequest: {request:?}");
        self.service
            .client()
            .add_prefix(request)
            .await
            .map_err(self.service.status("add prefix"))?;

        output::success(
            "add prefix",
            format_args!("Added prefix {} to {}.", cmd.prefix, cmd.config_name),
        );

        Ok(())
    }

    pub async fn remove_prefix(&mut self, cmd: RemovePrefixCmd) -> Result<(), Error> {
        let request = RemovePrefixRequest {
            name: cmd.config_name.clone(),
            prefix: cmd.prefix.addr().octets()[..12].to_vec(),
        };
        log::debug!("RemovePrefixRequest: {request:?}");
        self.service
            .client()
            .remove_prefix(request)
            .await
            .map_err(self.service.status("remove prefix"))?;

        output::success(
            "remove prefix",
            format_args!("Removed prefix {} from {}.", cmd.prefix, cmd.config_name),
        );

        Ok(())
    }

    pub async fn add_mapping(&mut self, cmd: AddMappingCmd) -> Result<(), Error> {
        let request = AddMappingRequest {
            name: cmd.config_name.clone(),
            ipv4: Some(IpAddress { addr: cmd.ipv4.octets().to_vec() }),
            ipv6: Some(IpAddress { addr: cmd.ipv6.octets().to_vec() }),
            prefix_index: cmd.prefix_index,
        };
        log::debug!("AddMappingRequest: {request:?}");
        self.service
            .client()
            .add_mapping(request)
            .await
            .map_err(self.service.status("add mapping"))?;

        output::success(
            "add mapping",
            format_args!(
                "Added mapping {} -> {} (prefix {}) to {}.",
                cmd.ipv4, cmd.ipv6, cmd.prefix_index, cmd.config_name
            ),
        );

        Ok(())
    }

    pub async fn remove_mapping(&mut self, cmd: RemoveMappingCmd) -> Result<(), Error> {
        let request = RemoveMappingRequest {
            name: cmd.config_name.clone(),
            ipv4: Some(IpAddress { addr: cmd.ipv4.octets().to_vec() }),
        };
        log::debug!("RemoveMappingRequest: {request:?}");
        self.service
            .client()
            .remove_mapping(request)
            .await
            .map_err(self.service.status("remove mapping"))?;

        output::success(
            "remove mapping",
            format_args!("Removed mapping for {} from {}.", cmd.ipv4, cmd.config_name),
        );

        Ok(())
    }

    pub async fn set_mtu(&mut self, cmd: MtuCmd) -> Result<(), Error> {
        let request = SetMtuRequest {
            name: cmd.config_name.clone(),
            mtu: Some(nat64pb::MtuConfig {
                ipv4_mtu: cmd.ipv4_mtu,
                ipv6_mtu: cmd.ipv6_mtu,
            }),
        };
        log::debug!("SetMtuRequest: {request:?}");
        self.service
            .client()
            .set_mtu(request)
            .await
            .map_err(self.service.status("set mtu"))?;

        output::success(
            "set mtu",
            format_args!(
                "Set MTU for {} (IPv4: {}, IPv6: {}).",
                cmd.config_name, cmd.ipv4_mtu, cmd.ipv6_mtu
            ),
        );

        Ok(())
    }

    pub async fn set_drop_unknown(&mut self, cmd: DropCmd) -> Result<(), Error> {
        let request = SetDropUnknownRequest {
            name: cmd.config_name.clone(),
            drop_unknown_prefix: cmd.drop_unknown_prefix,
            drop_unknown_mapping: cmd.drop_unknown_mapping,
        };
        log::debug!("SetDropUnknownRequest: {request:?}");
        self.service
            .client()
            .set_drop_unknown(request)
            .await
            .map_err(self.service.status("set drop"))?;

        output::success(
            "set drop",
            format_args!(
                "Set drop flags for {} (unknown prefix: {}, unknown mapping: {}).",
                cmd.config_name, cmd.drop_unknown_prefix, cmd.drop_unknown_mapping
            ),
        );

        Ok(())
    }
}

fn print_tree(resp: &ShowConfigResponse) {
    let mut tree = TreeBuilder::new("NAT64 Config".to_owned());

    if let Some(config) = &resp.config {
        tree.begin_child("Prefixes".to_owned());
        for (idx, prefix) in config.prefixes.iter().enumerate() {
            tree.add_empty_child(format!("{}: {:?}", idx, prefix.prefix));
        }
        tree.end_child();

        tree.begin_child("Mappings".to_owned());
        for mapping in &config.mappings {
            let ipv4 = mapping.ipv4.as_ref().map(|a| a.to_string()).unwrap_or_default();
            let ipv6 = mapping.ipv6.as_ref().map(|a| a.to_string()).unwrap_or_default();

            tree.add_empty_child(format!(
                "IPv4: {} -> IPv6: {} (prefix: {})",
                ipv4, ipv6, mapping.prefix_index
            ));
        }
        tree.end_child();

        if let Some(mtu) = &config.mtu {
            tree.begin_child("MTU".to_owned());
            tree.add_empty_child(format!("IPv4: {}", mtu.ipv4_mtu));
            tree.add_empty_child(format!("IPv6: {}", mtu.ipv6_mtu));
            tree.end_child();
        }

        tree.add_empty_child(format!("DropUnknownPrefix: {}", config.drop_unknown_prefix));
        tree.add_empty_child(format!("DropUnknownMapping: {}", config.drop_unknown_mapping));
    }

    let _ = ptree::print_tree(&tree.build());
}
