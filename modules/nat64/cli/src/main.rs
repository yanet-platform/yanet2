use core::net::{Ipv4Addr, Ipv6Addr};

use clap::{CommandFactory, Parser, Subcommand};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use nat64pb::{
    AddMappingRequest, AddPrefixRequest, Config, DeleteConfigRequest, ListConfigsRequest, RemoveMappingRequest,
    RemovePrefixRequest, SetDropUnknownRequest, SetMtuRequest, ShowConfigRequest,
    nat64_service_client::Nat64ServiceClient,
};
use netip::{Contiguous, Ipv6Network};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion, display,
    errors::Error,
    output,
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod nat64pb {
    tonic::include_proto!("modules.nat64.controlplane.nat64pb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.nat64.controlplane.nat64pb.v1.NAT64Service";

fn client(channel: LayeredChannel) -> Nat64ServiceClient<LayeredChannel> {
    Nat64ServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages nat64 module configs.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Subcommand)]
pub enum ModeCmd {
    /// List all NAT64 configurations.
    List,
    /// Show current configuration.
    Show(ShowConfigCmd),
    /// Delete a config.
    Delete(DeleteConfigCmd),
    /// Manage NAT64 prefixes.
    Prefix {
        #[clap(subcommand)]
        cmd: PrefixCmd,
    },
    /// Manage NAT64 mappings.
    Mapping {
        #[clap(subcommand)]
        cmd: MappingCmd,
    },
    /// Set MTU values.
    Mtu(MtuCmd),
    /// Set drop_unknown flags.
    Drop(DropCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Show(..) => "show",
            Self::Delete(..) => "delete",
            Self::Prefix { cmd: PrefixCmd::Add(..) } => "add prefix",
            Self::Prefix { cmd: PrefixCmd::Remove(..) } => "remove prefix",
            Self::Mapping { cmd: MappingCmd::Add(..) } => "add mapping",
            Self::Mapping { cmd: MappingCmd::Remove(..) } => "remove mapping",
            Self::Mtu(..) => "set mtu",
            Self::Drop(..) => "set drop",
        }
    }
}

#[derive(Debug, Clone, Subcommand)]
pub enum PrefixCmd {
    /// Add a new NAT64 prefix.
    Add(AddPrefixCmd),
    /// Remove NAT64 prefix.
    Remove(RemovePrefixCmd),
}

#[derive(Debug, Clone, Subcommand)]
pub enum MappingCmd {
    /// Add a new IPv4-IPv6 mapping.
    Add(AddMappingCmd),
    /// Remove IPv4-IPv6 mapping.
    Remove(RemoveMappingCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteConfigCmd {
    /// The name of the config to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct AddPrefixCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// IPv6 /96 prefix to add.
    #[arg(long, value_parser = parse_prefix)]
    pub prefix: Contiguous<Ipv6Network>,
}

#[derive(Debug, Clone, Parser)]
pub struct RemovePrefixCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// IPv6 /96 prefix to remove.
    #[arg(long, value_parser = parse_prefix)]
    pub prefix: Contiguous<Ipv6Network>,
}

#[derive(Debug, Clone, Parser)]
pub struct AddMappingCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// IPv4 address (4 bytes).
    #[arg(long, short = '4')]
    pub ipv4: Ipv4Addr,
    /// IPv6 address (16 bytes).
    #[arg(long, short = '6')]
    pub ipv6: Ipv6Addr,
    /// Index of the prefix to use.
    #[arg(long)]
    pub prefix_index: u32,
}

#[derive(Debug, Clone, Parser)]
pub struct RemoveMappingCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// IPv4 address (4 bytes).
    #[arg(long, short = '4')]
    pub ipv4: Ipv4Addr,
}

#[derive(Debug, Clone, Parser)]
pub struct MtuCmd {
    /// The name of the config to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
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
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Drop packets with unknown prefix.
    #[arg(long)]
    pub drop_unknown_prefix: bool,
    /// Drop packets with unknown mapping.
    #[arg(long)]
    pub drop_unknown_mapping: bool,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::List => list_configs(&mut service).await,
        ModeCmd::Show(cmd) => show_config(&mut service, cmd).await,
        ModeCmd::Delete(cmd) => delete_config(&mut service, cmd).await,
        ModeCmd::Prefix { cmd } => match cmd {
            PrefixCmd::Add(cmd) => add_prefix(&mut service, cmd).await,
            PrefixCmd::Remove(cmd) => remove_prefix(&mut service, cmd).await,
        },
        ModeCmd::Mapping { cmd } => match cmd {
            MappingCmd::Add(cmd) => add_mapping(&mut service, cmd).await,
            MappingCmd::Remove(cmd) => remove_mapping(&mut service, cmd).await,
        },
        ModeCmd::Mtu(cmd) => set_mtu(&mut service, cmd).await,
        ModeCmd::Drop(cmd) => set_drop_unknown(&mut service, cmd).await,
    }
}

type NAT64Service = Service<Nat64ServiceClient<LayeredChannel>>;

async fn list_configs(service: &mut NAT64Service) -> Result<(), Error> {
    let response = service
        .unary("list", ListConfigsRequest {}, async |client, request| {
            client.list_configs(request).await
        })
        .await?;

    output::data(
        || &response.configs,
        || {
            display::print_names_with_hint(
                &response.configs,
                format_args!("No NAT64 configurations found."),
                format_args!("create one with 'yanet-cli-nat64 prefix add --name <name> --prefix <cidr>'"),
            )
        },
    );

    Ok(())
}

async fn show_config(service: &mut NAT64Service, cmd: ShowConfigCmd) -> Result<(), Error> {
    let request = ShowConfigRequest { name: cmd.config_name.clone() };
    let response = service
        .unary_with(
            "show",
            request,
            service.not_found("show", &format!("config '{}'", cmd.config_name)),
            async |client, request| client.show_config(request).await,
        )
        .await?;

    output::data(
        || &response,
        || {
            let Some(config) = &response.config else {
                output::empty_with_hint(
                    format_args!("No NAT64 configuration found for '{}'.", cmd.config_name),
                    format_args!("create one with 'yanet-cli-nat64 prefix add --name <name> --prefix <cidr>'"),
                );
                return;
            };

            config_block(config).print();
        },
    );

    Ok(())
}

async fn delete_config(service: &mut NAT64Service, cmd: DeleteConfigCmd) -> Result<(), Error> {
    let request = DeleteConfigRequest { name: cmd.config_name.clone() };
    service
        .unary_with(
            "delete",
            request,
            service.not_found("delete", &format!("config '{}'", cmd.config_name)),
            async |client, request| client.delete_config(request).await,
        )
        .await?;

    output::success("delete", format_args!("Deleted config '{}'.", cmd.config_name));

    Ok(())
}

async fn add_prefix(service: &mut NAT64Service, cmd: AddPrefixCmd) -> Result<(), Error> {
    let request = AddPrefixRequest {
        name: cmd.config_name.clone(),
        prefix: Some(cmd.prefix.into()),
    };
    service
        .unary("add prefix", request, async |client, request| {
            client.add_prefix(request).await
        })
        .await?;

    output::success(
        "add prefix",
        format_args!("Added prefix {} to config '{}'.", cmd.prefix, cmd.config_name),
    );

    Ok(())
}

async fn remove_prefix(service: &mut NAT64Service, cmd: RemovePrefixCmd) -> Result<(), Error> {
    let request = RemovePrefixRequest {
        name: cmd.config_name.clone(),
        prefix: Some(cmd.prefix.into()),
    };
    service
        .unary("remove prefix", request, async |client, request| {
            client.remove_prefix(request).await
        })
        .await?;

    output::success(
        "remove prefix",
        format_args!("Removed prefix {} from config '{}'.", cmd.prefix, cmd.config_name),
    );

    Ok(())
}

async fn add_mapping(service: &mut NAT64Service, cmd: AddMappingCmd) -> Result<(), Error> {
    let request = AddMappingRequest {
        name: cmd.config_name.clone(),
        ipv4: Some(cmd.ipv4.into()),
        ipv6: Some(cmd.ipv6.into()),
        prefix_index: cmd.prefix_index,
    };
    service
        .unary("add mapping", request, async |client, request| {
            client.add_mapping(request).await
        })
        .await?;

    output::success(
        "add mapping",
        format_args!(
            "Added mapping {} -> {} (prefix {}) to config '{}'.",
            cmd.ipv4, cmd.ipv6, cmd.prefix_index, cmd.config_name
        ),
    );

    Ok(())
}

async fn remove_mapping(service: &mut NAT64Service, cmd: RemoveMappingCmd) -> Result<(), Error> {
    let request = RemoveMappingRequest {
        name: cmd.config_name.clone(),
        ipv4: Some(cmd.ipv4.into()),
    };
    service
        .unary("remove mapping", request, async |client, request| {
            client.remove_mapping(request).await
        })
        .await?;

    output::success(
        "remove mapping",
        format_args!("Removed mapping for {} from config '{}'.", cmd.ipv4, cmd.config_name),
    );

    Ok(())
}

async fn set_mtu(service: &mut NAT64Service, cmd: MtuCmd) -> Result<(), Error> {
    let request = SetMtuRequest {
        name: cmd.config_name.clone(),
        mtu: Some(nat64pb::MtuConfig {
            ipv4_mtu: cmd.ipv4_mtu,
            ipv6_mtu: cmd.ipv6_mtu,
        }),
    };
    service
        .unary("set mtu", request, async |client, request| {
            client.set_mtu(request).await
        })
        .await?;

    output::success(
        "set mtu",
        format_args!(
            "Set MTU on config '{}' (IPv4: {}, IPv6: {}).",
            cmd.config_name, cmd.ipv4_mtu, cmd.ipv6_mtu
        ),
    );

    Ok(())
}

async fn set_drop_unknown(service: &mut NAT64Service, cmd: DropCmd) -> Result<(), Error> {
    let request = SetDropUnknownRequest {
        name: cmd.config_name.clone(),
        drop_unknown_prefix: cmd.drop_unknown_prefix,
        drop_unknown_mapping: cmd.drop_unknown_mapping,
    };
    service
        .unary("set drop", request, async |client, request| {
            client.set_drop_unknown(request).await
        })
        .await?;

    output::success(
        "set drop",
        format_args!(
            "Set drop flags on config '{}' (unknown prefix: {}, unknown mapping: {}).",
            cmd.config_name, cmd.drop_unknown_prefix, cmd.drop_unknown_mapping
        ),
    );

    Ok(())
}

fn config_block(config: &Config) -> display::KeyValue {
    let prefixes = config
        .prefixes
        .iter()
        .enumerate()
        .map(|(idx, prefix)| format!("{idx}: {prefix}"));
    let mappings = config.mappings.iter().map(|mapping| {
        let ipv4 = mapping.ipv4.as_ref().map(|a| a.to_string()).unwrap_or_default();
        let ipv6 = mapping.ipv6.as_ref().map(|a| a.to_string()).unwrap_or_default();

        format!("{ipv4} -> {ipv6} (prefix {})", mapping.prefix_index)
    });

    let mut block = display::KeyValue::new()
        .rows("prefixes", prefixes)
        .rows("mappings", mappings);

    if let Some(mtu) = &config.mtu {
        block = block.row("mtu ipv4", mtu.ipv4_mtu).row("mtu ipv6", mtu.ipv6_mtu);
    }

    block
        .row("drop unknown prefix", config.drop_unknown_prefix)
        .row("drop unknown mapping", config.drop_unknown_mapping)
}

fn parse_prefix(value: &str) -> Result<Contiguous<Ipv6Network>, String> {
    let prefix = value
        .parse::<Contiguous<Ipv6Network>>()
        .map_err(|err| format!("invalid IPv6 prefix: {err}; expected /96"))?;

    if prefix.prefix() != 96 {
        return Err(format!("NAT64 prefix must use /96, got /{}", prefix.prefix()));
    }

    Ok(prefix)
}

fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn test_parse_prefix_accepts_96_network() {
        let prefix = parse_prefix("2001:db8:1234:5678::/96").expect("/96 prefix parses");

        assert_eq!(96, prefix.prefix());
    }

    #[test]
    fn test_parse_prefix_rejects_non_96_network() {
        let error = parse_prefix("2001:db8:1234:5678::/64").expect_err("/64 prefix rejected");

        assert!(error.contains("/96"));
    }
}
