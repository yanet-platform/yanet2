//! CLI for YANET route operator (route-side commands).
//!
//! Connects to a gRPC endpoint exposing the operator's `RouteService`
//! (the operator process directly, or the gateway once registration
//! has propagated) and drives the operator-owned RIB.

use core::{
    fmt::{self, Display, Formatter},
    net::IpAddr,
};
use std::collections::HashMap;

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
    client::{Connection, ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

use crate::operatorpb::{
    DeleteRouteRequest, FlushRoutesRequest, InsertRouteRequest, ListConfigsRequest, LookupRouteRequest, RouteSourceId,
    ShowRoutesRequest, readiness_service_client::ReadinessServiceClient, route_service_client::RouteServiceClient,
};

#[allow(clippy::all, non_snake_case)]
pub mod operatorpb {
    tonic::include_proto!("operators.route.operatorpb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "operators.route.operatorpb.v1.RouteService";

/// The fully-qualified gRPC readiness service name used in error messages.
const READINESS_SERVICE_NAME: &str = "operators.route.operatorpb.v1.ReadinessService";

/// Exit code used when the RPC succeeds but not all scopes are `STATE_READY`.
const EXIT_NOT_READY: i32 = 2;

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
    /// Show per-scope readiness of the route operator.
    Ready(ReadyCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ReadyCmd {
    /// Restrict output to these scope names; empty means all.
    pub scopes: Vec<String>,
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
    #[arg(long = "name", short = 'n')]
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

    match run(cmd).await {
        Ok(true) => {}
        Ok(false) => std::process::exit(EXIT_NOT_READY),
        Err(err) => {
            output::failure(&err);
            std::process::exit(err.exit_code());
        }
    }
}

/// Run the requested subcommand.
///
/// Returns `Ok(true)` when the RPC succeeded and every returned scope is
/// `STATE_READY`, `Ok(false)` when the RPC succeeded but at least one scope
/// is not ready, and `Err(_)` on transport or RPC failure.
async fn run(cmd: Cmd) -> Result<bool, Error> {
    let mut service = RouteService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await.map(|()| true),
        ModeCmd::Show(c) => service.show_routes(c).await.map(|()| true),
        ModeCmd::Lookup(c) => service.lookup_route(c).await.map(|()| true),
        ModeCmd::Insert(c) => service.insert_route(c).await.map(|()| true),
        ModeCmd::Remove(c) => service.remove_route(c).await.map(|()| true),
        ModeCmd::Flush(c) => service.flush_routes(c).await.map(|()| true),
        ModeCmd::Ready(c) => service.ready(c).await,
    }
}

pub struct RouteService {
    service: Service<RouteServiceClient<LayeredChannel>>,
    readiness: Service<ReadinessServiceClient<LayeredChannel>>,
}

impl RouteService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let conn = Connection::connect(connection).await?;
        let service = Service::new(&conn, SERVICE_NAME, |channel| {
            RouteServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });
        let readiness = Service::new(&conn, READINESS_SERVICE_NAME, |channel| {
            ReadinessServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });

        Ok(Self { service, readiness })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let response = self
            .service
            .client()
            .list_configs(ListConfigsRequest {})
            .await
            .map_err(self.service.status("list"))?
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
            .service
            .client()
            .show_routes(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();

        output::data(
            &response.routes,
            response.routes.is_empty(),
            format_args!("no routes in {}", cmd.name),
            || {
                let mut entries: Vec<RouteEntry> = response.routes.iter().cloned().map(RouteEntry::from).collect();
                entries.sort_by_key(|entry| entry.prefix.0);
                annotate_ecmp_groups(&mut entries);
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
            .service
            .client()
            .lookup_route(request)
            .await
            .map_err(self.service.status("lookup"))?
            .into_inner();

        output::data(
            &response.routes,
            response.routes.is_empty(),
            format_args!("no routes for {}", cmd.addr),
            || {
                let mut entries: Vec<RouteEntry> = response.routes.iter().cloned().map(RouteEntry::from).collect();
                annotate_ecmp_groups(&mut entries);
                print_route_table(entries);
            },
        );

        Ok(())
    }

    pub async fn insert_route(&mut self, cmd: RouteInsertCmd) -> Result<(), Error> {
        let nexthop_addrs = cmd.nexthop_addrs.iter().copied().map(Into::into).collect();

        let request = InsertRouteRequest {
            name: cmd.name.clone(),
            prefix: cmd.prefix.to_string(),
            nexthop_addrs,
            do_flush: true,
            source_id: cmd.source.to_proto().into(),
        };

        self.service
            .client()
            .insert_route(request)
            .await
            .map_err(self.service.status("insert"))?;

        let via = cmd
            .nexthop_addrs
            .iter()
            .map(|a| a.to_string())
            .collect::<Vec<_>>()
            .join(", ");

        output::success(
            "insert",
            format_args!(
                "Inserted {} via {} in {} (source: {}).",
                cmd.prefix,
                via,
                cmd.name,
                cmd.source.as_str()
            ),
        );

        Ok(())
    }

    pub async fn remove_route(&mut self, cmd: RouteRemoveCmd) -> Result<(), Error> {
        let nexthop_addrs = cmd.nexthop_addrs.iter().copied().map(Into::into).collect();

        let request = DeleteRouteRequest {
            name: cmd.name.clone(),
            prefix: cmd.prefix.to_string(),
            nexthop_addrs,
            do_flush: true,
            source_id: cmd.source.to_proto().into(),
        };

        self.service
            .client()
            .delete_route(request)
            .await
            .map_err(self.service.status("remove"))?;

        let via = cmd
            .nexthop_addrs
            .iter()
            .map(|a| a.to_string())
            .collect::<Vec<_>>()
            .join(", ");

        output::success(
            "remove",
            format_args!(
                "Removed {} via {} from {} (source: {}).",
                cmd.prefix,
                via,
                cmd.name,
                cmd.source.as_str()
            ),
        );

        Ok(())
    }

    pub async fn flush_routes(&mut self, cmd: RouteFlushCmd) -> Result<(), Error> {
        let request = FlushRoutesRequest { name: cmd.name.clone() };

        self.service
            .client()
            .flush_routes(request)
            .await
            .map_err(self.service.status("flush"))?;

        output::success("flush", format_args!("Flushed {}.", cmd.name));

        Ok(())
    }

    pub async fn ready(&mut self, cmd: ReadyCmd) -> Result<bool, Error> {
        let request = readinesspb::pb::ReadyRequest { scopes: cmd.scopes.clone() };

        let response = self
            .readiness
            .client()
            .ready(request)
            .await
            .map_err(self.readiness.status("ready"))?
            .into_inner();

        let returned_names: std::collections::HashSet<&str> =
            response.scopes.iter().map(|scope| scope.name.as_str()).collect();

        let missing: Vec<&str> = cmd
            .scopes
            .iter()
            .map(String::as_str)
            .filter(|name| !returned_names.contains(name))
            .collect();

        let all_scopes_ready = response
            .scopes
            .iter()
            .all(|scope| scope.state == readinesspb::pb::State::Ready as i32);

        let all_ready = all_scopes_ready && missing.is_empty();

        let total = response.scopes.len();
        let ready_count = response
            .scopes
            .iter()
            .filter(|scope| scope.state == readinesspb::pb::State::Ready as i32)
            .count();

        output::data(
            &response.scopes,
            response.scopes.is_empty() && missing.is_empty(),
            format_args!("no scopes"),
            || {
                let mut rows: Vec<ReadinessRow> = response.scopes.iter().map(ReadinessRow::from).collect();
                rows.sort_by(|a, b| a.scope.cmp(&b.scope));

                if !rows.is_empty() {
                    print_readiness_table(rows);
                }

                if !missing.is_empty() {
                    let missing_list = missing.join(", ");
                    let label = "missing (not registered):";

                    if output::is_colored() {
                        println!("{} {}", label.red(), missing_list.red());
                    } else {
                        println!("{label} {missing_list}");
                    }
                }

                let missing_count = missing.len();

                if missing_count > 0 {
                    println!("summary: {ready_count}/{total} ready, {missing_count} requested scope missing");
                } else {
                    println!("summary: {ready_count}/{total} ready");
                }
            },
        );

        Ok(all_ready)
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

/// Wraps a prefix with its best-route flag and ECMP group size.
///
/// `Ord` and `Eq` are by the address/prefix pair only; `is_best` and
/// `ecmp_size` are render-only hints intentionally excluded from identity.
/// `ecmp_size` is set to 1 initially and updated by `annotate_ecmp_groups`
/// when multiple best routes share the same prefix.
#[derive(Debug)]
pub struct Prefix(pub Contiguous<IpNetwork>, pub bool, pub usize);

impl Display for Prefix {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let Prefix(prefix, is_best, ecmp_size) = self;
        let s = prefix.to_string();

        if output::is_colored() {
            if *is_best {
                if *ecmp_size > 1 {
                    write!(f, "{} {}", s, "⇉".green())
                } else {
                    write!(f, "{s}")
                }
            } else {
                write!(f, "{}", s.truecolor(127, 127, 127))
            }
        } else if *is_best && *ecmp_size > 1 {
            write!(f, "{s} ⇉")
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
            prefix: Prefix(prefix, route.is_best, 1),
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

/// Annotates each `RouteEntry` in the slice with its ECMP group size.
///
/// An ECMP group is the set of best routes sharing the same prefix (across
/// all sources). When such a group has more than one member, the `ecmp_size`
/// field of each best `Prefix` in that group is set to the group count;
/// entries that are not best, or whose prefix has only one best route,
/// retain size 1 (unmarked).
fn annotate_ecmp_groups(entries: &mut [RouteEntry]) {
    let mut best_counts: HashMap<String, usize> = HashMap::new();

    for entry in entries.iter() {
        if entry.prefix.1 {
            let key = entry.prefix.0.to_string();
            *best_counts.entry(key).or_insert(0) += 1;
        }
    }

    for entry in entries.iter_mut() {
        if entry.prefix.1 {
            let key = entry.prefix.0.to_string();
            let count = best_counts.get(&key).copied().unwrap_or(1);
            entry.prefix.2 = count;
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

    ync::display::fit_terminal_width(&mut table);
    println!("{table}");
}

/// Wraps a readiness state for colored display in the table.
pub struct StateCell(readinesspb::pb::State);

impl Display for StateCell {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let StateCell(state) = self;
        let name = state.as_str_name().strip_prefix("STATE_").unwrap_or_default();

        if output::is_colored() {
            let colored = match state {
                readinesspb::pb::State::Ready => name.green().to_string(),
                readinesspb::pb::State::Degraded => name.yellow().to_string(),
                readinesspb::pb::State::NotReady => name.red().to_string(),
                readinesspb::pb::State::Unspecified | readinesspb::pb::State::Unknown => {
                    name.truecolor(127, 127, 127).to_string()
                }
            };
            write!(f, "{colored}")
        } else {
            write!(f, "{name}")
        }
    }
}

#[derive(Debug, Tabled)]
pub struct ReadinessRow {
    #[tabled(rename = "Scope")]
    pub scope: String,
    #[tabled(rename = "State")]
    pub state: String,
    #[tabled(rename = "Last Transition")]
    pub last_transition: String,
    #[tabled(rename = "Observed")]
    pub observed: String,
    #[tabled(rename = "Reasons")]
    pub reasons: String,
}

impl From<&readinesspb::pb::Scope> for ReadinessRow {
    fn from(scope: &readinesspb::pb::Scope) -> Self {
        let state = readinesspb::pb::State::try_from(scope.state).unwrap_or_default();
        let state_cell = StateCell(state);

        let reasons = scope
            .reasons
            .iter()
            .map(|reason| format!("{}: {}", reason.code, reason.message))
            .collect::<Vec<_>>()
            .join(", ");

        Self {
            scope: scope.name.clone(),
            state: state_cell.to_string(),
            last_transition: format_age(scope.last_transition_time.as_ref()),
            observed: format_age(scope.observed_at.as_ref()),
            reasons,
        }
    }
}

/// Formats a `prost_types::Timestamp` as a human-readable relative age.
///
/// Returns `"-"` when `ts` is `None` or the zero sentinel
/// (`seconds == 0 && nanos == 0`). Otherwise formats as `Xs ago`,
/// `XmYs ago`, or `XhYm ago` depending on magnitude.
pub fn format_age(ts: Option<&prost_types::Timestamp>) -> String {
    let ts = match ts {
        Some(ts) if ts.seconds != 0 || ts.nanos != 0 => ts,
        _ => return "-".to_string(),
    };

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default();

    let ts_secs = ts.seconds.max(0) as u64;
    let now_secs = now.as_secs();

    let secs = now_secs.saturating_sub(ts_secs);

    if secs < 60 {
        format!("{secs}s ago")
    } else if secs < 3600 {
        let minutes = secs / 60;
        let remainder = secs % 60;
        format!("{minutes}m{remainder}s ago")
    } else {
        let hours = secs / 3600;
        let minutes = (secs % 3600) / 60;
        format!("{hours}h{minutes}m ago")
    }
}

fn print_readiness_table(rows: Vec<ReadinessRow>) {
    let mut table = Table::new(&rows);
    table.with(
        Style::modern()
            .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
            .remove_horizontal(),
    );

    if output::is_colored() {
        table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));
        table.modify(Rows::first(), Color::BOLD);
    }

    ync::display::fit_terminal_width(&mut table);
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

#[cfg(test)]
mod test {
    use super::*;

    /// `--via ADDR PREFIX` must not consume the positional prefix as a second
    /// nexthop.
    #[test]
    fn insert_via_does_not_consume_prefix() {
        let cmd = Cmd::try_parse_from([
            "yanet-cli-operator-route",
            "insert",
            "--via",
            "192.0.2.1",
            "10.0.0.0/8",
            "-n",
            "cfg",
        ])
        .expect("parse must succeed");

        let ModeCmd::Insert(insert) = cmd.mode else {
            panic!("expected Insert variant");
        };

        assert_eq!("10.0.0.0/8", insert.prefix.to_string());
        assert_eq!(1, insert.nexthop_addrs.len());
        assert_eq!("192.0.2.1", insert.nexthop_addrs[0].to_string());
    }

    /// Repeating `--via` accumulates nexthops for ECMP routes.
    #[test]
    fn insert_via_repeated_accumulates_nexthops() {
        let cmd = Cmd::try_parse_from([
            "yanet-cli-operator-route",
            "insert",
            "--via",
            "192.0.2.1",
            "--via",
            "192.0.2.2",
            "10.0.0.0/8",
            "-n",
            "cfg",
        ])
        .expect("parse must succeed");

        let ModeCmd::Insert(insert) = cmd.mode else {
            panic!("expected Insert variant");
        };

        assert_eq!("10.0.0.0/8", insert.prefix.to_string());
        assert_eq!(2, insert.nexthop_addrs.len());
        assert_eq!("192.0.2.1", insert.nexthop_addrs[0].to_string());
        assert_eq!("192.0.2.2", insert.nexthop_addrs[1].to_string());
    }

    /// `--via ADDR PREFIX` in remove must not consume the positional prefix as
    /// a second nexthop.
    #[test]
    fn remove_via_does_not_consume_prefix() {
        let cmd = Cmd::try_parse_from([
            "yanet-cli-operator-route",
            "remove",
            "--via",
            "192.0.2.1",
            "10.0.0.0/8",
            "-n",
            "cfg",
        ])
        .expect("parse must succeed");

        let ModeCmd::Remove(remove) = cmd.mode else {
            panic!("expected Remove variant");
        };

        assert_eq!("10.0.0.0/8", remove.prefix.to_string());
        assert_eq!(1, remove.nexthop_addrs.len());
        assert_eq!("192.0.2.1", remove.nexthop_addrs[0].to_string());
    }

    #[test]
    fn format_age_none_returns_dash() {
        assert_eq!("-", format_age(None));
    }

    #[test]
    fn format_age_zero_sentinel_returns_dash() {
        let ts = prost_types::Timestamp { seconds: 0, nanos: 0 };
        assert_eq!("-", format_age(Some(&ts)));
    }

    #[test]
    fn format_age_seconds() {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap();
        let ts = prost_types::Timestamp {
            seconds: now.as_secs() as i64 - 30,
            nanos: 0,
        };
        assert_eq!("30s ago", format_age(Some(&ts)));
    }

    #[test]
    fn format_age_minutes() {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap();
        let ts = prost_types::Timestamp {
            seconds: now.as_secs() as i64 - 64,
            nanos: 0,
        };
        assert_eq!("1m4s ago", format_age(Some(&ts)));
    }

    #[test]
    fn format_age_hours() {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap();
        let ts = prost_types::Timestamp {
            seconds: now.as_secs() as i64 - (2 * 3600 + 3 * 60),
            nanos: 0,
        };
        assert_eq!("2h3m ago", format_age(Some(&ts)));
    }

    fn make_entry(prefix_str: &str, source: &str, is_best: bool) -> RouteEntry {
        let prefix = Contiguous::<IpNetwork>::parse(prefix_str).expect("must be valid prefix");
        RouteEntry {
            prefix: Prefix(prefix, is_best, 1),
            next_hop: String::new(),
            peer: String::new(),
            source: source.to_string(),
            peer_as: 0,
            origin_as: 0,
            pref: 0,
            med: 0,
            communities: Communities(vec![]),
        }
    }

    /// Two best routes sharing a prefix are marked with ECMP size 2; a prefix
    /// with one best and one non-best route remains at size 1; a single best
    /// route remains at size 1.
    #[test]
    fn annotate_ecmp_groups_marks_multi_best_prefixes() {
        let mut entries = vec![
            make_entry("10.0.0.0/8", "static", true),
            make_entry("10.0.0.0/8", "static", true),
            make_entry("192.168.0.0/24", "static", true),
            make_entry("192.168.0.0/24", "static", false),
            make_entry("172.16.0.0/12", "static", true),
        ];

        annotate_ecmp_groups(&mut entries);

        assert_eq!(2, entries[0].prefix.2);
        assert_eq!(2, entries[1].prefix.2);
        assert_eq!(1, entries[2].prefix.2);
        assert_eq!(1, entries[3].prefix.2);
        assert_eq!(1, entries[4].prefix.2);
    }

    /// One best `static` route and one best `bird` route on the same prefix
    /// ARE grouped — `BuildFIB` merges best routes from all sources into one
    /// FIB entry, so the CLI reflects the actual forwarding group width.
    #[test]
    fn annotate_ecmp_groups_different_sources_are_grouped() {
        let mut entries = vec![
            make_entry("10.0.0.0/8", "static", true),
            make_entry("10.0.0.0/8", "bird", true),
        ];

        annotate_ecmp_groups(&mut entries);

        assert_eq!(2, entries[0].prefix.2);
        assert_eq!(2, entries[1].prefix.2);
    }

    /// Two best routes on the same prefix from the same source form an ECMP
    /// group of size 2.
    #[test]
    fn annotate_ecmp_groups_same_source_grouped() {
        let mut entries = vec![
            make_entry("10.0.0.0/8", "bird", true),
            make_entry("10.0.0.0/8", "bird", true),
        ];

        annotate_ecmp_groups(&mut entries);

        assert_eq!(2, entries[0].prefix.2);
        assert_eq!(2, entries[1].prefix.2);
    }
}
