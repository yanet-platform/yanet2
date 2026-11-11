use clap::{CommandFactory, Parser, ValueEnum, value_parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::partition_prefixes;
use dscppb::{
    Config, DeleteConfigRequest, DscpConfig, ShowConfigRequest, UpdateConfigRequest,
    dscp_service_client::DscpServiceClient, dscp_service_server::SERVICE_NAME,
};
use netip::{Contiguous, IpNetwork};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion, display,
    errors::Error,
    output,
};

use crate::dscppb::ListConfigsRequest;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod dscppb {
    tonic::include_proto!("modules.dscp.controlplane.dscppb.v1");
}

/// How to create a config: an update carries the whole config, its marking
/// included.
const CREATE_CONFIG_HINT: &str =
    "create one with 'yanet-cli-dscp update --name <name> --prefix <cidr> --flag <flag> --mark <mark>'";

/// Manages dscp module configs.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List configs.
    List,
    /// Show a config.
    Show(ShowConfigCmd),
    /// Create or replace a config.
    Update(UpdateConfigCmd),
    /// Delete a config.
    Delete(DeleteConfigCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Show(..) => "show",
            Self::Update(..) => "update",
            Self::Delete(..) => "delete",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// DSCP module name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateConfigCmd {
    /// DSCP module name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Destination prefixes to mark, replacing the current set entirely.
    #[arg(long = "prefix", short = 'p', required_unless_present = "source_prefixes", num_args = 0..)]
    pub prefixes: Vec<Contiguous<IpNetwork>>,
    /// Source prefixes to mark, intersected with any destination prefixes.
    #[arg(long = "source-prefix", required_unless_present = "prefixes", num_args = 0..)]
    pub source_prefixes: Vec<Contiguous<IpNetwork>>,
    /// When the DSCP field is rewritten.
    #[arg(long, value_enum)]
    pub flag: MarkingFlag,
    /// DSCP mark value in 0..=63.
    #[arg(long, value_parser = value_parser!(u32).range(0..=63))]
    pub mark: u32,
}

/// The marking mode of a DSCP config, carried on the wire as its number.
#[derive(Debug, Clone, Copy, ValueEnum)]
pub enum MarkingFlag {
    /// Never rewrite the DSCP field.
    Never,
    /// Rewrite the DSCP field only when the original value is 0.
    Default,
    /// Always rewrite the DSCP field.
    Always,
}

impl From<MarkingFlag> for u32 {
    fn from(flag: MarkingFlag) -> Self {
        match flag {
            MarkingFlag::Never => 0,
            MarkingFlag::Default => 1,
            MarkingFlag::Always => 2,
        }
    }
}

impl From<UpdateConfigCmd> for UpdateConfigRequest {
    fn from(cmd: UpdateConfigCmd) -> Self {
        let (prefixes4, prefixes6) = partition_prefixes(cmd.prefixes);
        let (source_prefixes4, source_prefixes6) = partition_prefixes(cmd.source_prefixes);
        Self {
            name: cmd.config_name,
            config: Some(Config {
                prefixes4,
                prefixes6,
                dscp_config: Some(DscpConfig {
                    flag: cmd.flag.into(),
                    mark: cmd.mark,
                }),
                source_prefixes4,
                source_prefixes6,
            }),
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteConfigCmd {
    /// DSCP module name to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

fn client(channel: LayeredChannel) -> DscpServiceClient<LayeredChannel> {
    DscpServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Zstd)
        .accept_compressed(CompressionEncoding::Zstd)
        .accept_compressed(CompressionEncoding::Gzip)
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
        ModeCmd::Update(cmd) => update_config(&mut service, cmd).await,
        ModeCmd::Delete(cmd) => delete_config(&mut service, cmd).await,
    }
}

type DscpService = Service<DscpServiceClient<LayeredChannel>>;

async fn list_configs(service: &mut DscpService) -> Result<(), Error> {
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
                format_args!("No DSCP configurations found."),
                format_args!("{CREATE_CONFIG_HINT}"),
            )
        },
    );

    Ok(())
}

async fn show_config(service: &mut DscpService, cmd: ShowConfigCmd) -> Result<(), Error> {
    let request = ShowConfigRequest { name: cmd.config_name.to_owned() };
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
                    format_args!("No DSCP configuration found for '{}'.", cmd.config_name),
                    format_args!("{CREATE_CONFIG_HINT}"),
                );
                return;
            };

            config_block(config).print();
        },
    );

    Ok(())
}

async fn update_config(service: &mut DscpService, cmd: UpdateConfigCmd) -> Result<(), Error> {
    let name = cmd.config_name.clone();
    let request = UpdateConfigRequest::from(cmd);
    service
        .unary("update", request, async |client, request| {
            client.update_config(request).await
        })
        .await?;

    output::success("update", format_args!("Updated config '{name}'."));

    Ok(())
}

async fn delete_config(service: &mut DscpService, cmd: DeleteConfigCmd) -> Result<(), Error> {
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

fn config_block(config: &Config) -> display::KeyValue {
    let mut block = display::KeyValue::new();

    if let Some(marking) = config.dscp_config {
        block = block
            .row("flag", flag_to_string(marking.flag))
            .row("mark", format!("{} (0x{:02x})", marking.mark, marking.mark));
    }

    let prefixes = config
        .prefixes4
        .iter()
        .map(ToString::to_string)
        .chain(config.prefixes6.iter().map(ToString::to_string));

    let source_prefixes = config
        .source_prefixes4
        .iter()
        .map(ToString::to_string)
        .chain(config.source_prefixes6.iter().map(ToString::to_string));

    block
        .rows("prefixes", prefixes)
        .rows("source prefixes", source_prefixes)
}

fn flag_to_string(flag: u32) -> String {
    match flag {
        0 => "Never".to_string(),
        1 => "Default (only if original DSCP is 0)".to_string(),
        2 => "Always".to_string(),
        _ => format!("Unknown ({flag})"),
    }
}

fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}

#[cfg(test)]
mod test {
    use commonpb::pb::{IPv4Prefix, IPv6Prefix};

    use super::*;

    #[test]
    fn test_update_config_partitions_directions_and_families() {
        let request = UpdateConfigRequest::from(UpdateConfigCmd {
            config_name: "mark".into(),
            prefixes: vec!["198.51.100.0/24".parse().unwrap(), "2001:db8:2::/48".parse().unwrap()],
            source_prefixes: vec!["2001:db8:1::/48".parse().unwrap(), "192.0.2.0/24".parse().unwrap()],
            flag: MarkingFlag::Always,
            mark: 46,
        });
        let config = request.config.unwrap();
        assert_eq!("mark", request.name);
        assert_eq!(vec!["198.51.100.0/24".parse::<IPv4Prefix>().unwrap()], config.prefixes4);
        assert_eq!(vec!["2001:db8:2::/48".parse::<IPv6Prefix>().unwrap()], config.prefixes6);
        assert_eq!(
            vec!["192.0.2.0/24".parse::<IPv4Prefix>().unwrap()],
            config.source_prefixes4
        );
        assert_eq!(
            vec!["2001:db8:1::/48".parse::<IPv6Prefix>().unwrap()],
            config.source_prefixes6
        );
        assert_eq!(Some(DscpConfig { flag: 2, mark: 46 }), config.dscp_config);
    }

    #[test]
    fn test_update_config_sources_are_optional() {
        let request = UpdateConfigRequest::from(UpdateConfigCmd {
            config_name: "mark".into(),
            prefixes: vec!["198.51.100.0/24".parse().unwrap()],
            source_prefixes: vec![],
            flag: MarkingFlag::Default,
            mark: 46,
        });
        let config = request.config.unwrap();
        assert_eq!(vec!["198.51.100.0/24".parse::<IPv4Prefix>().unwrap()], config.prefixes4);
        assert!(config.source_prefixes4.is_empty());
        assert!(config.source_prefixes6.is_empty());
    }
}
