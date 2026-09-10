use clap::{CommandFactory, Parser, ValueEnum, value_parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::partition_prefixes;
use dscppb::{
    AddPrefixesRequest, Config, DeleteConfigRequest, DscpConfig, RemovePrefixesRequest, SetDscpMarkingRequest,
    ShowConfigRequest, dscp_service_client::DscpServiceClient,
};
use netip::{Contiguous, IpNetwork};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{ConnectionArgs, LayeredChannel, Service},
    completion, display,
    errors::Error,
    output,
};

use crate::dscppb::ListConfigsRequest;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod dscppb {
    tonic::include_proto!("modules.dscp.controlplane.dscppb.v1");
}

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
    /// Add prefixes to the input filter of a config.
    PrefixAdd(AddPrefixesCmd),
    /// Remove prefixes from the input filter of a config.
    PrefixRemove(RemovePrefixesCmd),
    /// Set the DSCP marking of a config.
    SetMarking(SetDscpMarkingCmd),
    /// Delete a config.
    Delete(DeleteConfigCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Show(..) => "show",
            Self::PrefixAdd(..) => "prefix-add",
            Self::PrefixRemove(..) => "prefix-remove",
            Self::SetMarking(..) => "set-marking",
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
pub struct AddPrefixesCmd {
    /// DSCP module name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Prefix to be added to the input filter of the DSCP module.
    #[arg(long, short = 'p', required = true)]
    pub prefix: Vec<Contiguous<IpNetwork>>,
}

#[derive(Debug, Clone, Parser)]
pub struct RemovePrefixesCmd {
    /// DSCP module name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Prefix to be removed from the input filter of the DSCP module.
    #[arg(long, short = 'p', required = true)]
    pub prefix: Vec<Contiguous<IpNetwork>>,
}

#[derive(Debug, Clone, Parser)]
pub struct SetDscpMarkingCmd {
    /// DSCP module name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
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

#[derive(Debug, Clone, Parser)]
pub struct DeleteConfigCmd {
    /// DSCP module name to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.dscp.controlplane.dscppb.v1.DscpService";

fn client(channel: LayeredChannel) -> DscpServiceClient<LayeredChannel> {
    DscpServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = DscpService::new(&cmd.globals.connection, action).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::PrefixAdd(cmd) => service.add_prefixes(cmd).await,
        ModeCmd::PrefixRemove(cmd) => service.remove_prefixes(cmd).await,
        ModeCmd::SetMarking(cmd) => service.set_dscp_marking(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
    }
}

pub struct DscpService {
    service: Service<DscpServiceClient<LayeredChannel>>,
}

impl DscpService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, client).await?;

        Ok(Self { service })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let response = self
            .service
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
                    format_args!("create one with 'yanet-cli-dscp prefix-add --name <name> --prefix <cidr>'"),
                )
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.to_owned() };
        let response = self
            .service
            .unary_with(
                "show",
                request,
                self.service.not_found("show", &format!("config '{}'", cmd.config_name)),
                async |client, request| client.show_config(request).await,
            )
            .await?;

        output::data(
            || &response,
            || {
                let Some(config) = &response.config else {
                    output::empty_with_hint(
                        format_args!("No DSCP configuration found for '{}'.", cmd.config_name),
                        format_args!("create one with 'yanet-cli-dscp prefix-add --name <name> --prefix <cidr>'"),
                    );
                    return;
                };

                config_block(config).print();
            },
        );

        Ok(())
    }

    pub async fn add_prefixes(&mut self, cmd: AddPrefixesCmd) -> Result<(), Error> {
        let (prefixes4, prefixes6) = partition_prefixes(cmd.prefix.iter().copied());
        let request = AddPrefixesRequest {
            name: cmd.config_name.clone(),
            prefixes4,
            prefixes6,
        };
        self.service
            .unary("prefix-add", request, async |client, request| {
                client.add_prefixes(request).await
            })
            .await?;

        output::success(
            "prefix-add",
            format_args!("Added {} prefix(es) to config '{}'.", cmd.prefix.len(), cmd.config_name),
        );

        Ok(())
    }

    pub async fn remove_prefixes(&mut self, cmd: RemovePrefixesCmd) -> Result<(), Error> {
        let (prefixes4, prefixes6) = partition_prefixes(cmd.prefix.iter().copied());
        let request = RemovePrefixesRequest {
            name: cmd.config_name.clone(),
            prefixes4,
            prefixes6,
        };
        self.service
            .unary("prefix-remove", request, async |client, request| {
                client.remove_prefixes(request).await
            })
            .await?;

        output::success(
            "prefix-remove",
            format_args!(
                "Removed {} prefix(es) from config '{}'.",
                cmd.prefix.len(),
                cmd.config_name
            ),
        );

        Ok(())
    }

    pub async fn set_dscp_marking(&mut self, cmd: SetDscpMarkingCmd) -> Result<(), Error> {
        let request = SetDscpMarkingRequest {
            name: cmd.config_name.clone(),
            dscp_config: Some(DscpConfig {
                flag: cmd.flag.into(),
                mark: cmd.mark,
            }),
        };
        self.service
            .unary("set-marking", request, async |client, request| {
                client.set_dscp_marking(request).await
            })
            .await?;

        output::success(
            "set-marking",
            format_args!("Set marking on config '{}'.", cmd.config_name),
        );

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteConfigCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.service
            .unary_with(
                "delete",
                request,
                self.service
                    .not_found("delete", &format!("config '{}'", cmd.config_name)),
                async |client, request| client.delete_config(request).await,
            )
            .await?;

        output::success("delete", format_args!("Deleted config '{}'.", cmd.config_name));

        Ok(())
    }
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

    block.rows("prefixes", prefixes)
}

fn flag_to_string(flag: u32) -> String {
    match flag {
        0 => "Never".to_string(),
        1 => "Default (only if original DSCP is 0)".to_string(),
        2 => "Always".to_string(),
        _ => format!("Unknown ({flag})"),
    }
}

/// Completion candidates for a `--name` argument: the dscp configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}
