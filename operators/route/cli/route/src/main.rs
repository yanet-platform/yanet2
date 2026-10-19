//! CLI for YANET route operator (route-side commands).
//!
//! Connects to a gRPC endpoint exposing the operator's `RouteService`
//! (the operator process directly, or the gateway once registration
//! has propagated) and drives the operator-owned RIB.

use core::net::IpAddr;

use clap::{CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::IpPrefix;
use netip::{Contiguous, IpNetwork};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion, display,
    errors::Error,
    output,
};

use crate::operatorpb::{
    DeleteRouteRequest, FlushRoutesRequest, InsertRouteRequest, ListConfigsRequest, LookupRouteRequest, RouteSourceId,
    ShowRoutesRequest, route_service_client::RouteServiceClient, route_service_server::SERVICE_NAME,
};

mod render;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod operatorpb {
    tonic::include_proto!("operators.route.operatorpb.v1");
}

fn client(channel: LayeredChannel) -> RouteServiceClient<LayeredChannel> {
    RouteServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Zstd)
        .accept_compressed(CompressionEncoding::Zstd)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages the RIB of the route operator.
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
    /// List all RIB configurations known to the operator.
    List,
    /// Show routes currently stored in RIB.
    Show(RouteShowCmd),
    /// Perform RIB route lookup.
    Lookup(RouteLookupCmd),
    /// Insert a unicast static route.
    Insert(RouteInsertCmd),
    /// Remove a unicast static route.
    Remove(RouteRemoveCmd),
    /// Flush RIB to FIB for a configuration.
    Flush(RouteFlushCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Show(..) => "show",
            Self::Lookup(..) => "lookup",
            Self::Insert(..) => "insert",
            Self::Remove(..) => "remove",
            Self::Flush(..) => "flush",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct RouteShowCmd {
    /// Show only IPv4 routes.
    #[arg(long, short = '4', conflicts_with = "ipv6")]
    pub ipv4: bool,
    /// Show only IPv6 routes.
    #[arg(long, short = '6')]
    pub ipv6: bool,
    /// Configuration name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteLookupCmd {
    /// IP address to look up.
    pub addr: IpAddr,
    /// Configuration name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteInsertCmd {
    /// Destination prefix in CIDR notation.
    pub prefix: Contiguous<IpNetwork>,
    /// Configuration name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub name: String,
    /// Next-hop IP address(es); repeat `--via` to specify multiple nexthops for
    /// ECMP.
    #[arg(long = "via", required = true)]
    pub nexthop_addrs: Vec<IpAddr>,
    /// Route source type (static or bird). Defaults to static.
    #[arg(long = "source", default_value = "static")]
    pub source: RouteSource,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteRemoveCmd {
    /// Destination prefix in CIDR notation.
    pub prefix: Contiguous<IpNetwork>,
    /// Configuration name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub name: String,
    /// Next-hop IP address(es); repeat `--via` to specify multiple nexthops for
    /// ECMP.
    #[arg(long = "via", required = true)]
    pub nexthop_addrs: Vec<IpAddr>,
    /// Route source type (static or bird). Defaults to static.
    #[arg(long = "source", default_value = "static")]
    pub source: RouteSource,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteFlushCmd {
    /// Configuration name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub name: String,
}

#[derive(Debug, Clone, clap::ValueEnum)]
pub enum RouteSource {
    Static,
    Bird,
}

impl RouteSource {
    fn to_proto(&self) -> RouteSourceId {
        match self {
            Self::Static => RouteSourceId::Static,
            Self::Bird => RouteSourceId::Bird,
        }
    }

    fn as_str(&self) -> &'static str {
        match self {
            Self::Static => "static",
            Self::Bird => "bird",
        }
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}

/// Run the requested subcommand.
///
/// Returns `Ok(())` when the RPC succeeded, `Err(_)` on transport or RPC
/// failure.
async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::List => list_configs(&mut service).await,
        ModeCmd::Show(c) => show_routes(&mut service, c).await,
        ModeCmd::Lookup(c) => lookup_route(&mut service, c).await,
        ModeCmd::Insert(c) => insert_route(&mut service, c).await,
        ModeCmd::Remove(c) => remove_route(&mut service, c).await,
        ModeCmd::Flush(c) => flush_routes(&mut service, c).await,
    }
}

type RouteService = Service<RouteServiceClient<LayeredChannel>>;

async fn list_configs(service: &mut RouteService) -> Result<(), Error> {
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
                format_args!("No route configurations found."),
                format_args!("create one with 'yanet-cli-operator-route insert <prefix> --name <name> --via <addr>'"),
            )
        },
    );

    Ok(())
}

async fn show_routes(service: &mut RouteService, cmd: RouteShowCmd) -> Result<(), Error> {
    let request = ShowRoutesRequest {
        name: cmd.name.clone(),
        ipv4_only: cmd.ipv4,
        ipv6_only: cmd.ipv6,
    };

    let response = service
        .unary_with(
            "show",
            request,
            service.not_found("show", &format!("config '{}'", cmd.name)),
            async |client, request| client.show_routes(request).await,
        )
        .await?;

    output::data(|| &response.routes, || render::show_routes(&response.routes, &cmd.name));

    Ok(())
}

async fn lookup_route(service: &mut RouteService, cmd: RouteLookupCmd) -> Result<(), Error> {
    let request = LookupRouteRequest {
        name: cmd.name.clone(),
        ip_addr: Some(cmd.addr.into()),
    };

    let response = service
        .unary("lookup", request, async |client, request| {
            client.lookup_route(request).await
        })
        .await?;

    output::data(|| &response.routes, || render::lookup_route(&response.routes, cmd.addr));

    Ok(())
}

async fn insert_route(service: &mut RouteService, cmd: RouteInsertCmd) -> Result<(), Error> {
    let nexthop_addrs = cmd.nexthop_addrs.iter().copied().map(Into::into).collect();

    let request = InsertRouteRequest {
        name: cmd.name.clone(),
        prefix: Some(IpPrefix::from(cmd.prefix)),
        nexthop_addrs,
        do_flush: true,
        source_id: cmd.source.to_proto().into(),
    };

    service
        .unary("insert", request, async |client, request| {
            client.insert_route(request).await
        })
        .await?;

    let via = cmd
        .nexthop_addrs
        .iter()
        .map(|a| a.to_string())
        .collect::<Vec<_>>()
        .join(", ");

    output::success(
        "insert",
        format_args!(
            "Inserted {} via {} in config '{}' (source: {}).",
            cmd.prefix,
            via,
            cmd.name,
            cmd.source.as_str()
        ),
    );

    Ok(())
}

async fn remove_route(service: &mut RouteService, cmd: RouteRemoveCmd) -> Result<(), Error> {
    let nexthop_addrs = cmd.nexthop_addrs.iter().copied().map(Into::into).collect();

    let request = DeleteRouteRequest {
        name: cmd.name.clone(),
        prefix: Some(IpPrefix::from(cmd.prefix)),
        nexthop_addrs,
        do_flush: true,
        source_id: cmd.source.to_proto().into(),
    };

    service
        .unary("remove", request, async |client, request| {
            client.delete_route(request).await
        })
        .await?;

    let via = cmd
        .nexthop_addrs
        .iter()
        .map(|a| a.to_string())
        .collect::<Vec<_>>()
        .join(", ");

    output::success(
        "remove",
        format_args!(
            "Removed {} via {} from config '{}' (source: {}).",
            cmd.prefix,
            via,
            cmd.name,
            cmd.source.as_str()
        ),
    );

    Ok(())
}

async fn flush_routes(service: &mut RouteService, cmd: RouteFlushCmd) -> Result<(), Error> {
    let request = FlushRoutesRequest { name: cmd.name.clone() };

    service
        .unary("flush", request, async |client, request| {
            client.flush_routes(request).await
        })
        .await?;

    output::success("flush", format_args!("Flushed config '{}'.", cmd.name));

    Ok(())
}

/// Serializes the `source` field of `Route` as a lowercase string name
/// (e.g. `"static"`, `"bird"`) instead of the raw `i32` enum discriminant.
pub fn serialize_route_source<S>(value: &i32, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    serializer.serialize_str(&render::route_source_name(*value))
}
