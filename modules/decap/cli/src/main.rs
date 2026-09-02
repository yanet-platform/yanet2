use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::partition_prefixes;
use decappb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, ShowConfigResponse, UpdateConfigRequest,
    decap_service_client::DecapServiceClient,
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

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod decappb {
    use serde::Serialize;

    tonic::include_proto!("modules.decap.controlplane.decappb.v1");
}

/// Decap module.
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
    Update(UpdateConfigCmd),
    /// Delete a decap module config.
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
    /// Decap module name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateConfigCmd {
    /// Decap module name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Prefixes in the full desired set, replacing the current one entirely.
    #[arg(long = "prefix", short = 'p', alias = "prefixes", required = true, num_args = 0..)]
    pub prefixes: Vec<Contiguous<IpNetwork>>,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteConfigCmd {
    /// Decap module name to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.decap.controlplane.decappb.v1.DecapService";

/// Maps a genuine "config not found" status into a friendly message.
const NOT_FOUND: NotFoundMapper = NotFoundMapper::new(SERVICE_NAME, "requested config");

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = DecapService::new(&cmd.connection, action).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
    }
}

pub struct DecapService {
    service: Service<DecapServiceClient<LayeredChannel>>,
}

impl DecapService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, |channel| {
            DecapServiceClient::new(channel)
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
                        format_args!("No decap configurations found."),
                        format_args!("create one with 'yanet-cli-decap update --name <name> --prefix <cidr>'"),
                    );
                    return;
                }

                let mut tree = TreeBuilder::new("List Decap Configs".to_string());
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
                if response.prefixes4.is_empty() && response.prefixes6.is_empty() {
                    output::empty_with_hint(
                        format_args!("No decap prefixes found for '{}'.", cmd.config_name),
                        format_args!("create one with 'yanet-cli-decap update --name <name> --prefix <cidr>'"),
                    );
                    return;
                }

                print_tree(&response);
            },
        );

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateConfigCmd) -> Result<(), Error> {
        let (prefixes4, prefixes6) = partition_prefixes(cmd.prefixes);
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            prefixes4,
            prefixes6,
        };
        log::trace!("update config request: {request:?}");
        let response = self
            .service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?
            .into_inner();
        log::debug!("update config response: {response:?}");

        output::success("update", format_args!("Updated decap {}.", cmd.config_name));

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteConfigCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        log::trace!("delete config request: {request:?}");
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
        log::debug!("delete config response: {response:?}");

        output::success("delete", format_args!("Deleted decap {}.", cmd.config_name));

        Ok(())
    }
}

fn print_tree(resp: &ShowConfigResponse) {
    let mut tree = TreeBuilder::new("Decap Prefixes".to_string());

    let prefixes = resp
        .prefixes4
        .iter()
        .map(ToString::to_string)
        .chain(resp.prefixes6.iter().map(ToString::to_string));
    for (idx, prefix) in prefixes.enumerate() {
        tree.add_empty_child(format!("{idx}: {prefix}"));
    }

    let _ = ptree::print_tree(&tree.build());
}

/// Completion candidates for a `--name` argument: the decap configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            DecapServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
}
