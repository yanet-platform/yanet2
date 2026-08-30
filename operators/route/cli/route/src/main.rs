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
use clap_complete::{
    CompleteEnv,
    engine::{ArgValueCandidates, CompletionCandidate},
};
use colored::Colorize;
use commonpb::pb::IpPrefix;
use netip::{Contiguous, IpNetwork};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{Connection, ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::Error,
    output::{self, CommonFormat},
};

use crate::operatorpb::{
    DeleteRouteRequest, FlushRoutesRequest, InsertRouteRequest, ListConfigsRequest, LookupRouteRequest, RouteSourceId,
    ShowRoutesRequest, route_service_client::RouteServiceClient,
};

#[allow(clippy::all, clippy::std_instead_of_core, non_snake_case)]
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

fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    start();
}

#[tokio::main(flavor = "current_thread")]
async fn start() {
    let cmd = Cmd::parse();

    ync::init(cmd.verbose, cmd.format);

    match run(cmd).await {
        Ok(()) => {}
        Err(err) => {
            output::failure(&err);
            std::process::exit(err.exit_code());
        }
    }
}

/// Completion candidates for a `--name` argument: the route operator
/// configs the operator currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            RouteServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
}

/// Run the requested subcommand.
///
/// Returns `Ok(())` when the RPC succeeded, `Err(_)` on transport or RPC
/// failure.
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
    service: Service<RouteServiceClient<LayeredChannel>>,
}

impl RouteService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let conn = Connection::connect(connection).await?;
        let service = Service::new(&conn, SERVICE_NAME, |channel| {
            RouteServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });

        Ok(Self { service })
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
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No route configurations found."),
                        format_args!(
                            "create one with 'yanet-cli-operator-route insert <prefix> --name <name> --via <addr>'"
                        ),
                    );
                    return;
                }

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
            || &response.routes,
            || {
                if response.routes.is_empty() {
                    output::empty(format_args!("No routes found for '{}'.", cmd.name));
                    return;
                }

                let mut entries: Vec<RouteEntry> = response.routes.iter().cloned().map(RouteEntry::from).collect();
                entries.sort_by(|a, b| a.prefix.0.cmp(&b.prefix.0));
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
            || &response.routes,
            || {
                if response.routes.is_empty() {
                    output::empty(format_args!("No routes found for {}.", cmd.addr));
                    return;
                }

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
            prefix: Some(IpPrefix::from(cmd.prefix)),
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
            prefix: Some(IpPrefix::from(cmd.prefix)),
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

/// A destination prefix as reported by the server: either successfully
/// decoded, or the debug rendering of the wire message that failed to
/// decode.
///
/// The malformed case keeps a rendering of the original message instead of
/// discarding it, so two different malformed values never compare equal and
/// are never grouped into the same ECMP set. Every parsed value orders
/// before every malformed one. Malformed values order lexicographically by
/// that rendering.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum PrefixValue {
    Parsed(Contiguous<IpNetwork>),
    Malformed(String),
}

impl Display for PrefixValue {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        match self {
            Self::Parsed(prefix) => write!(f, "{prefix}"),
            Self::Malformed(_) => f.write_str("invalid"),
        }
    }
}

/// Wraps a prefix value with its best-route flag and ECMP group size.
///
/// `Ord` and `Eq` delegate to the wrapped `PrefixValue`. `is_best` and
/// `ecmp_size` are render-only hints intentionally excluded from identity.
/// `ecmp_size` is set to 1 initially and updated by `annotate_ecmp_groups`
/// when multiple best routes share the same prefix.
#[derive(Debug)]
pub struct Prefix(pub PrefixValue, pub bool, pub usize);

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
                write!(f, "{}", output::paint_dim(&s))
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
    #[tabled(rename = "Global ID")]
    pub global_id: u32,
    #[tabled(rename = "Ifindex")]
    pub ifindex: u32,
    #[tabled(rename = "Communities")]
    pub communities: Communities,
}

impl From<operatorpb::Route> for RouteEntry {
    fn from(route: operatorpb::Route) -> Self {
        let communities = route.large_communities.into_iter().map(|c| c.into()).collect();
        let prefix = match route.prefix.as_ref().map(Contiguous::<IpNetwork>::try_from) {
            Some(Ok(prefix)) => PrefixValue::Parsed(prefix),
            _ => PrefixValue::Malformed(format!("{:?}", route.prefix)),
        };

        Self {
            prefix: Prefix(prefix, route.is_best, 1),
            next_hop: route.next_hop.as_ref().map(|a| a.to_string()).unwrap_or_default(),
            peer: route.peer.as_ref().map(|a| a.to_string()).unwrap_or_default(),
            source: route_source_name(route.source),
            peer_as: route.peer_as,
            origin_as: route.origin_as,
            pref: route.pref,
            med: route.med,
            global_id: route.global_id,
            ifindex: route.ifindex,
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
    let mut best_counts: HashMap<PrefixValue, usize> = HashMap::new();

    for entry in entries.iter() {
        if entry.prefix.1 {
            *best_counts.entry(entry.prefix.0.clone()).or_insert(0) += 1;
        }
    }

    for entry in entries.iter_mut() {
        if entry.prefix.1 {
            let count = best_counts.get(&entry.prefix.0).copied().unwrap_or(1);
            entry.prefix.2 = count;
        }
    }
}

fn print_route_table(entries: Vec<RouteEntry>) {
    ync::display::print_table_from_entries(entries);
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

#[cfg(test)]
mod test {
    use super::*;

    /// `prefix`/`next_hop`/`peer` need no `serialize_with` override: plain
    /// `Option<commonpb::pb::IpPrefix>` and
    /// `Option<commonpb::pb::IpAddress>` fields already serialize as the
    /// CIDR or plain address string, or `null` when absent, through those
    /// types' own `Serialize` impls. This pins that output byte-for-byte,
    /// including that the structured prefix still renders as the same
    /// `"10.0.0.0/8"` string the wire string field used to produce.
    #[test]
    fn route_prefix_next_hop_and_peer_serialize_without_a_field_override() {
        let route = operatorpb::Route {
            prefix: Some("10.0.0.0/8".parse().unwrap()),
            next_hop: Some(commonpb::pb::IpAddress::from(IpAddr::V4(core::net::Ipv4Addr::new(
                192, 0, 2, 1,
            )))),
            peer: None,
            ..Default::default()
        };

        let json = serde_json::to_string(&route).unwrap();

        assert_eq!(
            r#"{"prefix":"10.0.0.0/8","next_hop":"192.0.2.1","peer":null,"route_distinguisher":0,"peer_as":0,"origin_as":0,"med":0,"pref":0,"as_path_len":0,"source":"unknown","large_communities":[],"is_best":false,"global_id":0,"ifindex":0}"#,
            json
        );
    }

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

    fn entry_with(prefix: PrefixValue, source: &str, is_best: bool) -> RouteEntry {
        RouteEntry {
            prefix: Prefix(prefix, is_best, 1),
            next_hop: String::new(),
            peer: String::new(),
            source: source.to_string(),
            peer_as: 0,
            origin_as: 0,
            pref: 0,
            med: 0,
            global_id: 1,
            ifindex: 1,
            communities: Communities(vec![]),
        }
    }

    fn make_entry(prefix_str: &str, source: &str, is_best: bool) -> RouteEntry {
        let prefix = Contiguous::<IpNetwork>::parse(prefix_str).expect("must be valid prefix");
        entry_with(PrefixValue::Parsed(prefix), source, is_best)
    }

    fn make_malformed_entry(prefix_str: &str, source: &str, is_best: bool) -> RouteEntry {
        entry_with(PrefixValue::Malformed(prefix_str.to_string()), source, is_best)
    }

    /// A prefix the server sends as a message that cannot be decoded --
    /// missing, an unset or address-less branch, or a prefix length past
    /// the family bound -- converts into a `RouteEntry` carrying the
    /// malformed marker, instead of aborting the process.
    #[test]
    fn route_entry_from_malformed_prefix_does_not_panic() {
        let malformed = [
            None,
            Some(IpPrefix { prefix: None }),
            Some(IpPrefix {
                prefix: Some(commonpb::pb::ip_prefix::Prefix::V4(commonpb::pb::IPv4Prefix {
                    addr: None,
                    prefix_len: 8,
                })),
            }),
            Some(IpPrefix {
                prefix: Some(commonpb::pb::ip_prefix::Prefix::V4(commonpb::pb::IPv4Prefix {
                    addr: Some(commonpb::pb::IPv4Address { addr: 0x0a000000 }),
                    prefix_len: 33,
                })),
            }),
        ];

        for prefix in malformed {
            let route = operatorpb::Route {
                prefix,
                is_best: true,
                ..Default::default()
            };

            let entry = RouteEntry::from(route);

            assert!(
                matches!(entry.prefix.0, PrefixValue::Malformed(..)),
                "expected a malformed prefix, got {:?}",
                entry.prefix.0
            );
            assert_eq!("invalid", entry.prefix.0.to_string());
        }
    }

    /// Two prefixes that both fail to decode stay distinct values, so
    /// `annotate_ecmp_groups` never folds unrelated malformed routes into
    /// one ECMP group.
    #[test]
    fn route_entry_malformed_prefixes_stay_distinct() {
        let entry_of = |addr: u32| {
            RouteEntry::from(operatorpb::Route {
                prefix: Some(IpPrefix {
                    prefix: Some(commonpb::pb::ip_prefix::Prefix::V4(commonpb::pb::IPv4Prefix {
                        addr: Some(commonpb::pb::IPv4Address { addr }),
                        prefix_len: 33,
                    })),
                }),
                ..Default::default()
            })
        };

        assert_ne!(entry_of(1).prefix.0, entry_of(2).prefix.0);
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

    /// Two malformed prefixes with different raw strings stay in separate
    /// ECMP groups — the same raw string groups together like a real
    /// duplicate.
    #[test]
    fn annotate_ecmp_groups_keeps_distinct_malformed_prefixes_apart() {
        let mut entries = vec![
            make_malformed_entry("garbage-a", "static", true),
            make_malformed_entry("garbage-b", "static", true),
            make_malformed_entry("garbage-a", "static", true),
        ];

        annotate_ecmp_groups(&mut entries);

        assert_eq!(2, entries[0].prefix.2);
        assert_eq!(1, entries[1].prefix.2);
        assert_eq!(2, entries[2].prefix.2);
    }

    #[test]
    fn cmd_is_valid() {
        Cmd::command().debug_assert();
    }
}
