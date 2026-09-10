use clap::{CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::partition_prefixes;
use decappb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, ShowConfigResponse, UpdateConfigRequest,
    decap_service_client::DecapServiceClient,
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

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod decappb {
    tonic::include_proto!("modules.decap.controlplane.decappb.v1");
}

/// Manages decap module configs.
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

fn client(channel: LayeredChannel) -> DecapServiceClient<LayeredChannel> {
    DecapServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = DecapService::new(&cmd.globals.connection, action).await?;

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
                    format_args!("No decap configurations found."),
                    format_args!("create one with 'yanet-cli-decap update --name <name> --prefix <cidr>'"),
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
                if response.prefixes4.is_empty() && response.prefixes6.is_empty() {
                    output::empty_with_hint(
                        format_args!("No decap prefixes found for '{}'.", cmd.config_name),
                        format_args!("create one with 'yanet-cli-decap update --name <name> --prefix <cidr>'"),
                    );
                    return;
                }

                config_block(&response).print();
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
        self.service
            .unary("update", request, async |client, request| {
                client.update_config(request).await
            })
            .await?;

        output::success("update", format_args!("Updated config '{}'.", cmd.config_name));

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

fn config_block(response: &ShowConfigResponse) -> display::KeyValue {
    let prefixes = response
        .prefixes4
        .iter()
        .map(ToString::to_string)
        .chain(response.prefixes6.iter().map(ToString::to_string));

    display::KeyValue::new().rows("prefixes", prefixes)
}

/// Completion candidates for a `--name` argument: the decap configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}
