//! CLI for YANET route operator (route-side commands).
//!
//! Connects to a gRPC endpoint exposing the operator's `RouteService`
//! (the operator process directly, or the gateway once registration
//! has propagated) and drives the operator-owned RIB.

use core::{
    fmt::{self, Display, Formatter},
    net::IpAddr,
};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use colored::Colorize;
use netip::{Contiguous, IpNetwork};
use tabled::{
    Table, Tabled,
    settings::{
        Color, Style,
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
    },
};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    errors::Error,
    output::{self, CommonFormat},
};

use crate::operatorpb::{
    DeleteRouteRequest, FlushRoutesRequest, InsertRouteRequest, ListConfigsRequest, LookupRouteRequest, RouteSourceId,
    ShowRoutesRequest, route_service_client::RouteServiceClient,
};

#[allow(clippy::all, non_snake_case)]
pub mod operatorpb {
    tonic::include_proto!("operators.route.operatorpb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "operators.route.operatorpb.v1.RouteService";

/// Route operator CLI (RIB management).
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
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

#[derive(Debug, Clone, Parser)]
pub struct RouteShowCmd {
    /// Show only IPv4 routes.
    #[arg(long)]
    pub ipv4: bool,
    /// Show only IPv6 routes.
    #[arg(long)]
    pub ipv6: bool,
    /// Configuration name.
    #[arg(long = "name", short = 'n')]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteLookupCmd {
    /// IP address to look up.
    pub addr: IpAddr,
    /// Configuration name.
    #[arg(long = "name", short = 'n')]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteInsertCmd {
    /// Destination prefix in CIDR notation.
    pub prefix: Contiguous<IpNetwork>,
    /// Configuration name.
    #[arg(long = "name", short = 'n')]
    pub name: String,
    /// Next-hop IP address.
    #[arg(long = "via")]
    pub nexthop_addr: IpAddr,
    /// Route source type (static or bird). Defaults to static.
    #[arg(long = "source", default_value = "static")]
    pub source: RouteSource,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteRemoveCmd {
    /// Destination prefix in CIDR notation.
    pub prefix: Contiguous<IpNetwork>,
    /// Configuration name.
    #[arg(long = "name", short = 'n')]
    pub name: String,
    /// Next-hop IP address.
    #[arg(long = "via")]
    pub nexthop_addr: IpAddr,
    /// Route source type (static or bird). Defaults to static.
    #[arg(long = "source", default_value = "static")]
    pub source: RouteSource,
}

#[derive(Debug, Clone, Parser)]
pub struct RouteFlushCmd {
    /// Configuration name.
    #[arg(long = "name", short = 'n')]
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
    let mut service = RouteService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(c) => service.show_routes(c).await,
        ModeCmd::Lookup(c) => service.lookup_route(c).await,
        ModeCmd::Insert(c) => service.insert_route(c).await,
        ModeCmd::Remove(c) => service.remove_route(c).await,
        ModeCmd::Flush(c) => service.flush_routes(c).await,
    }
}

pub struct RouteService {
    client: RouteServiceClient<LayeredChannel>,
    endpoint: String,
}

impl RouteService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|e| Error::from_connection(e, "connect", &connection.endpoint))?;
        let client = RouteServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);

        Ok(Self {
            client,
            endpoint: connection.endpoint.clone(),
        })
    }

    fn map_err<'a>(&'a self, action: &'a str) -> impl FnOnce(tonic::Status) -> Error + 'a {
        let endpoint = self.endpoint.clone();
        move |status| Error::from_status(status, action, endpoint, SERVICE_NAME)
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let response = self
            .client
            .list_configs(ListConfigsRequest {})
            .await
            .map_err(self.map_err("list"))?
            .into_inner();

        output::data(
            &response.configs,
            response.configs.is_empty(),
            format_args!("no configurations"),
            || {
                for config in &response.configs {
                    println!("{config}");
                }
            },
        );

        Ok(())
    }

    pub async fn show_routes(&mut self, cmd: RouteShowCmd) -> Result<(), Error> {
        let request = ShowRoutesRequest {
            name: cmd.name.clone(),
            ipv4_only: cmd.ipv4,
            ipv6_only: cmd.ipv6,
        };

        let response = self
            .client
            .show_routes(request)
            .await
            .map_err(self.map_err("show"))?
            .into_inner();

        output::data(
            &response.routes,
            response.routes.is_empty(),
            format_args!("no routes in {}", cmd.name),
            || {
                let mut entries: Vec<RouteEntry> = response.routes.iter().cloned().map(RouteEntry::from).collect();
                entries.sort_by_key(|entry| entry.prefix.0);
                print_route_table(entries);
            },
        );

        Ok(())
    }

    pub async fn lookup_route(&mut self, cmd: RouteLookupCmd) -> Result<(), Error> {
        let request = LookupRouteRequest {
            name: cmd.name.clone(),
            ip_addr: Some(cmd.addr.into()),
        };

        let response = self
            .client
            .lookup_route(request)
            .await
            .map_err(self.map_err("lookup"))?
            .into_inner();

        output::data(
            &response.routes,
            response.routes.is_empty(),
            format_args!("no routes for {}", cmd.addr),
            || {
                let entries: Vec<RouteEntry> = response.routes.iter().cloned().map(RouteEntry::from).collect();
                print_route_table(entries);
            },
        );

        Ok(())
    }

    pub async fn insert_route(&mut self, cmd: RouteInsertCmd) -> Result<(), Error> {
        let request = InsertRouteRequest {
            name: cmd.name.clone(),
            prefix: cmd.prefix.to_string(),
            nexthop_addr: Some(cmd.nexthop_addr.into()),
            do_flush: true,
            source_id: cmd.source.to_proto().into(),
        };

        self.client
            .insert_route(request)
            .await
            .map_err(self.map_err("insert"))?;

        output::success(
            "insert",
            format_args!(
                "inserted {} via {} in {} (source: {})",
                cmd.prefix,
                cmd.nexthop_addr,
                cmd.name,
                cmd.source.as_str()
            ),
        );

        Ok(())
    }

    pub async fn remove_route(&mut self, cmd: RouteRemoveCmd) -> Result<(), Error> {
        let request = DeleteRouteRequest {
            name: cmd.name.clone(),
            prefix: cmd.prefix.to_string(),
            nexthop_addr: Some(cmd.nexthop_addr.into()),
            do_flush: true,
            source_id: cmd.source.to_proto().into(),
        };

        self.client
            .delete_route(request)
            .await
            .map_err(self.map_err("remove"))?;

        output::success(
            "remove",
            format_args!(
                "removed {} via {} from {} (source: {})",
                cmd.prefix,
                cmd.nexthop_addr,
                cmd.name,
                cmd.source.as_str()
            ),
        );

        Ok(())
    }

    pub async fn flush_routes(&mut self, cmd: RouteFlushCmd) -> Result<(), Error> {
        let request = FlushRoutesRequest { name: cmd.name.clone() };

        self.client.flush_routes(request).await.map_err(self.map_err("flush"))?;

        output::success("flush", format_args!("flushed {}", cmd.name));

        Ok(())
    }
}

#[derive(Debug)]
pub struct LargeCommunity {
    pub global_administrator: u32,
    pub local_data_part1: u32,
    pub local_data_part2: u32,
}

impl From<operatorpb::LargeCommunity> for LargeCommunity {
    fn from(community: operatorpb::LargeCommunity) -> Self {
        Self {
            global_administrator: community.global_administrator,
            local_data_part1: community.local_data_part1,
            local_data_part2: community.local_data_part2,
        }
    }
}

impl Display for LargeCommunity {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        write!(
            f,
            "{}:{}:{}",
            self.global_administrator, self.local_data_part1, self.local_data_part2
        )
    }
}

#[derive(Debug)]
pub struct Communities(pub Vec<LargeCommunity>);

impl Display for Communities {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let Self(communities) = self;
        let strings: Vec<String> = communities.iter().map(|c| c.to_string()).collect();
        write!(f, "{}", strings.join(" "))
    }
}

/// Wraps a prefix with its best-route flag.
///
/// `Ord` and `Eq` are by the address/prefix pair only; the `is_best` field
/// is a render-only hint and intentionally excluded from identity.
#[derive(Debug)]
pub struct Prefix(pub Contiguous<IpNetwork>, pub bool);

impl Display for Prefix {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let Prefix(prefix, is_best) = self;
        let s = prefix.to_string();

        if output::is_colored() && !is_best {
            write!(f, "{}", s.truecolor(127, 127, 127))
        } else {
            write!(f, "{s}")
        }
    }
}

impl PartialOrd for Prefix {
    fn partial_cmp(&self, other: &Self) -> Option<core::cmp::Ordering> {
        Some(self.cmp(other))
    }
}

impl Ord for Prefix {
    fn cmp(&self, other: &Self) -> core::cmp::Ordering {
        self.0.cmp(&other.0)
    }
}

impl PartialEq for Prefix {
    fn eq(&self, other: &Self) -> bool {
        self.0 == other.0
    }
}

impl Eq for Prefix {}

#[derive(Debug, Tabled)]
pub struct RouteEntry {
    #[tabled(rename = "Prefix")]
    pub prefix: Prefix,
    #[tabled(rename = "Next Hop")]
    pub next_hop: String,
    #[tabled(rename = "Peer")]
    pub peer: String,
    #[tabled(rename = "Source")]
    pub source: String,
    #[tabled(rename = "Peer AS")]
    pub peer_as: u32,
    #[tabled(rename = "Origin")]
    pub origin_as: u32,
    #[tabled(rename = "Pref")]
    pub pref: u32,
    #[tabled(rename = "MED")]
    pub med: u32,
    #[tabled(rename = "Communities")]
    pub communities: Communities,
}

impl From<operatorpb::Route> for RouteEntry {
    fn from(route: operatorpb::Route) -> Self {
        let communities = route.large_communities.into_iter().map(|c| c.into()).collect();
        let prefix = Contiguous::<IpNetwork>::parse(&route.prefix).expect("must be valid prefix");

        Self {
            prefix: Prefix(prefix, route.is_best),
            next_hop: route.next_hop.as_ref().map(|a| a.to_string()).unwrap_or_default(),
            peer: route.peer.as_ref().map(|a| a.to_string()).unwrap_or_default(),
            source: route_source_name(route.source),
            peer_as: route.peer_as,
            origin_as: route.origin_as,
            pref: route.pref,
            med: route.med,
            communities: Communities(communities),
        }
    }
}

fn print_route_table(entries: Vec<RouteEntry>) {
    let mut table = Table::new(&entries);
    table.with(
        Style::modern()
            .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
            .remove_horizontal(),
    );

    if output::is_colored() {
        table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));
        table.modify(Rows::first(), Color::BOLD);
    }

    println!("{table}");
}

/// Returns the lowercase display name for a `RouteSourceId` discriminant.
///
/// Converts a raw `i32` source value to its lowercase string name by calling
/// `as_str_name` on the corresponding `RouteSourceId` variant.
fn route_source_name(value: i32) -> String {
    RouteSourceId::try_from(value)
        .unwrap_or_default()
        .as_str_name()
        .strip_prefix("ROUTE_SOURCE_ID_")
        .unwrap_or_default()
        .to_lowercase()
}

/// Serializes the `source` field of `Route` as a lowercase string name
/// (e.g. `"static"`, `"bird"`) instead of the raw `i32` enum discriminant.
pub fn serialize_route_source<S>(value: &i32, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    serializer.serialize_str(&route_source_name(*value))
}

/// Serializes an optional `IpAddress` field as a string (e.g. `"10.0.0.1"`)
/// or JSON `null` when absent.
pub fn serialize_ip_addr<S>(value: &Option<commonpb::pb::IpAddress>, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    match value {
        Some(addr) => serializer.serialize_str(&addr.to_string()),
        None => serializer.serialize_none(),
    }
}
