use clap::{ArgAction, CommandFactory, Parser, ValueEnum, value_parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::partition_prefixes;
use dscppb::{
    AddPrefixesRequest, DeleteConfigRequest, DscpConfig, RemovePrefixesRequest, SetDscpMarkingRequest,
    ShowConfigRequest, ShowConfigResponse, dscp_service_client::DscpServiceClient,
};
use netip::{Contiguous, IpNetwork};
use ptree::TreeBuilder;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::{Error, NotFoundMapper},
    output::{self, CommonFormat},
};

use crate::dscppb::ListConfigsRequest;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod dscppb {
    use serde::Serialize;

    tonic::include_proto!("modules.dscp.controlplane.dscppb.v1");
}

/// DSCP module for packet marking.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
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

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    List,
    Show(ShowConfigCmd),
    PrefixAdd(AddPrefixesCmd),
    PrefixRemove(RemovePrefixesCmd),
    SetMarking(SetDscpMarkingCmd),
    /// Delete a dscp module config.
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

/// Maps a genuine "config not found" status into a friendly message.
const NOT_FOUND: NotFoundMapper = NotFoundMapper::new(SERVICE_NAME, "requested config");

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = DscpService::new(&cmd.connection, action).await?;

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
        let service = Service::connect_for(connection, action, SERVICE_NAME, |channel| {
            DscpServiceClient::new(channel)
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
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No DSCP configurations found."),
                        format_args!("create one with 'yanet-cli-dscp prefix-add --name <name> --prefix <cidr>'"),
                    );
                    return;
                }

                let mut tree = TreeBuilder::new("List DSCP Configs".to_string());
                for config in &response.configs {
                    tree.add_empty_child(config.clone());
                }
                let _ = ptree::print_tree(&tree.build());
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.to_owned() };
        log::trace!("show config request: {request:?}");
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();
        log::debug!("show config response: {response:?}");

        output::data(
            || &response,
            || {
                if response.config.is_none() {
                    output::empty_with_hint(
                        format_args!("No DSCP configuration found for '{}'.", cmd.config_name),
                        format_args!("create one with 'yanet-cli-dscp prefix-add --name <name> --prefix <cidr>'"),
                    );
                    return;
                }

                print_tree(&response);
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
        log::trace!("AddPrefixesRequest: {request:?}");
        let response = self
            .service
            .client()
            .add_prefixes(request)
            .await
            .map_err(self.service.status("prefix-add"))?
            .into_inner();
        log::debug!("AddPrefixesResponse: {response:?}");

        output::success(
            "prefix-add",
            format_args!("Added {} prefix(es) to {}.", cmd.prefix.len(), cmd.config_name),
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
        log::trace!("RemovePrefixesRequest: {request:?}");
        let response = self
            .service
            .client()
            .remove_prefixes(request)
            .await
            .map_err(self.service.status("prefix-remove"))?
            .into_inner();
        log::debug!("RemovePrefixesResponse: {response:?}");

        output::success(
            "prefix-remove",
            format_args!("Removed {} prefix(es) from {}.", cmd.prefix.len(), cmd.config_name),
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
        log::trace!("SetDscpMarkingRequest: {request:?}");
        let response = self
            .service
            .client()
            .set_dscp_marking(request)
            .await
            .map_err(self.service.status("set-marking"))?
            .into_inner();
        log::debug!("SetDscpMarkingResponse: {response:?}");

        output::success("set-marking", format_args!("Set DSCP marking on {}.", cmd.config_name));

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteConfigCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        log::trace!("DeleteConfigRequest: {request:?}");
        let response = self
            .service
            .client()
            .delete_config(request)
            .await
            .map_err(|status| {
                NOT_FOUND.map(
                    status,
                    "delete",
                    self.service.endpoint(),
                    Some(&format!("config '{}'", cmd.config_name)),
                )
            })?
            .into_inner();
        log::debug!("DeleteConfigResponse: {response:?}");

        output::success("delete", format_args!("Deleted dscp {}.", cmd.config_name));

        Ok(())
    }
}

fn print_tree(response: &ShowConfigResponse) {
    let mut tree = TreeBuilder::new("View DSCP Config".to_string());

    if let Some(config) = &response.config {
        if let Some(dscp_config) = config.dscp_config {
            tree.begin_child("DSCP Marking".to_string());
            tree.add_empty_child(format!("Flag: {}", flag_to_string(dscp_config.flag)));
            tree.add_empty_child(format!("Mark: {} (0x{:02x})", dscp_config.mark, dscp_config.mark));
            tree.end_child();
        }

        tree.begin_child("Prefixes".to_string());
        if config.prefixes4.is_empty() && config.prefixes6.is_empty() {
            tree.add_empty_child("(none)".to_owned());
        }

        let prefixes = config
            .prefixes4
            .iter()
            .map(ToString::to_string)
            .chain(config.prefixes6.iter().map(ToString::to_string));
        for (idx, prefix) in prefixes.enumerate() {
            tree.add_empty_child(format!("{idx}: {prefix}"));
        }
        tree.end_child();
    }

    let _ = ptree::print_tree(&tree.build());
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
    completion::candidates(
        Cmd::command,
        |channel| {
            DscpServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
}
