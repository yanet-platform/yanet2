//! CLI for YANET "route-mpls" module.

use core::net::IpAddr;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod routemplspb {
    use serde::Serialize;

    tonic::include_proto!("modules.route_mpls.controlplane.routemplspb.v1");
}

use clap::{CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use netip::{Contiguous, IpNetwork};
use routemplspb::{
    CreateConfigRequest, DeleteConfigRequest, ListConfigsRequest, NextHop, Rule, ShowConfigRequest,
    UpdateConfigRequest, UpdateEvent, route_mpls_service_client::RouteMplsServiceClient, update_event::Event,
};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{ConnectionArgs, LayeredChannel, Service},
    completion, display,
    errors::Error,
    output,
};

/// Manages route-mpls module configs.
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
    /// Show the MPLS routes of a config.
    Show(RouteShowCmd),
    /// Create a config.
    Create(RouteCreateCmd),
    /// Delete a config.
    Delete(RouteDeleteCmd),
    /// Update a route in a config.
    Update(RouteUpdateCmd),
    /// Withdraw a route from a config.
    Withdraw(RouteWithdrawCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Show(..) => "show",
            Self::Create(..) => "create",
            Self::Delete(..) => "delete",
            Self::Update(..) => "update",
            Self::Withdraw(..) => "withdraw",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct RouteShowCmd {
    /// Route config name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteCreateCmd {
    /// Route config name.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteDeleteCmd {
    /// Route config name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteUpdateCmd {
    /// Route config name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Route prefix.
    #[arg(long = "prefix", short = 'p')]
    pub prefix: Contiguous<IpNetwork>,
    /// The IP address of the tunnel destination.
    #[arg(long = "dst")]
    pub dst_addr: IpAddr,
    /// The MPLS Label to encapsulate packets into.
    #[arg(long = "label", value_parser = clap::value_parser!(u32).range(0..=1_048_575))]
    pub mpls_label: u32,
    /// The IP address of the tunnel source.
    #[arg(long = "src")]
    pub src_addr: IpAddr,
    /// The ECMP weight.
    #[arg(long = "weight")]
    pub weight: u64,
    /// Nexthop counter name.
    #[arg(long = "counter")]
    pub counter: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteWithdrawCmd {
    /// Route config name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Route prefix.
    #[arg(long = "prefix", short = 'p')]
    pub prefix: Contiguous<IpNetwork>,
    /// The IP address of the tunnel destination.
    #[arg(long = "dst")]
    pub dst_addr: IpAddr,
    /// The MPLS Label to encapsulate packets into.
    #[arg(long = "label", value_parser = clap::value_parser!(u32).range(0..=1_048_575))]
    pub mpls_label: u32,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.route_mpls.controlplane.routemplspb.v1.RouteMPLSService";

fn client(channel: LayeredChannel) -> RouteMplsServiceClient<LayeredChannel> {
    RouteMplsServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

/// Completion candidates for a `--name` argument: the route-mpls configs
/// the module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = RouteMplsService::new(&cmd.globals.connection, action).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Create(cmd) => service.create_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_route(cmd).await,
        ModeCmd::Withdraw(cmd) => service.withdraw_route(cmd).await,
    }
}

pub struct RouteMplsService {
    service: Service<RouteMplsServiceClient<LayeredChannel>>,
}

impl RouteMplsService {
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
                    format_args!("No route-mpls configurations found."),
                    format_args!("create one with 'yanet-cli-route-mpls create --name <name>'"),
                )
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: RouteShowCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
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
                print!(
                    "{}",
                    serde_yaml::to_string(&response).expect("route-mpls config YAML serialization must not fail")
                );

                if response.rules.is_empty() {
                    output::empty_with_hint(
                        format_args!("No route-mpls rules found for '{}'.", cmd.config_name),
                        format_args!(
                            "create one with 'yanet-cli-route-mpls update --name <name> --prefix <cidr> --dst <addr> --label <n> --src <addr> --weight <n> --counter <counter-name>'"
                        ),
                    );
                }
            },
        );

        Ok(())
    }

    pub async fn create_config(&mut self, cmd: RouteCreateCmd) -> Result<(), Error> {
        let request = CreateConfigRequest {
            name: cmd.config_name.clone(),
            rules: Vec::<Rule>::new(),
        };
        self.service
            .unary("create", request, async |client, request| {
                client.create_config(request).await
            })
            .await?;

        output::success("create", format_args!("Created config '{}'.", cmd.config_name));

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: RouteDeleteCmd) -> Result<(), Error> {
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

    pub async fn update_route(&mut self, cmd: RouteUpdateCmd) -> Result<(), Error> {
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            updates: vec![UpdateEvent {
                event: Some(Event::Update(Rule {
                    prefix: Some(cmd.prefix.into()),
                    nexthop: Some(NextHop {
                        kind: routemplspb::ActionKind::Tunnel.into(),
                        label: cmd.mpls_label,
                        source_ip: Some(cmd.src_addr.into()),
                        destination_ip: Some(cmd.dst_addr.into()),
                        weight: cmd.weight,
                        counter: cmd.counter,
                    }),
                })),
            }],
        };
        self.service
            .unary("update", request, async |client, request| {
                client.update_config(request).await
            })
            .await?;

        output::success("update", format_args!("Updated route in config '{}'.", cmd.config_name));

        Ok(())
    }

    pub async fn withdraw_route(&mut self, cmd: RouteWithdrawCmd) -> Result<(), Error> {
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            updates: vec![UpdateEvent {
                event: Some(Event::Withdraw(Rule {
                    prefix: Some(cmd.prefix.into()),
                    nexthop: Some(NextHop {
                        kind: routemplspb::ActionKind::Tunnel.into(),
                        label: cmd.mpls_label,
                        source_ip: None,
                        destination_ip: Some(cmd.dst_addr.into()),
                        weight: 0,
                        counter: "".to_string(),
                    }),
                })),
            }],
        };
        self.service
            .unary("withdraw", request, async |client, request| {
                client.update_config(request).await
            })
            .await?;

        output::success(
            "withdraw",
            format_args!("Withdrew route from config '{}'.", cmd.config_name),
        );

        Ok(())
    }
}
