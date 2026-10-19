use core::{
    fmt::{self, Display, Formatter},
    net::IpAddr,
};
use std::collections::HashMap;

use colored::Colorize;
use commonpb::serde_with;
use netip::{Contiguous, IpNetwork};
use tabled::Tabled;
use ync::output;

use crate::operatorpb::{self, RouteSourceId};

pub fn show_routes(routes: &[operatorpb::Route], name: &str) {
    if routes.is_empty() {
        output::empty(format_args!("No routes found for '{}'.", name));
        return;
    }

    let mut entries: Vec<RouteEntry> = routes.iter().cloned().map(RouteEntry::from).collect();
    entries.sort_by(|a, b| a.prefix.0.cmp(&b.prefix.0));
    annotate_ecmp_groups(&mut entries);
    print_route_table(entries);
}

pub fn lookup_route(routes: &[operatorpb::Route], addr: IpAddr) {
    if routes.is_empty() {
        output::empty(format_args!("No routes found for {}.", addr));
        return;
    }

    let mut entries: Vec<RouteEntry> = routes.iter().cloned().map(RouteEntry::from).collect();
    annotate_ecmp_groups(&mut entries);
    print_route_table(entries);
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

/// Keeps malformed wire prefixes distinct while ordering parsed values first.
///
/// Malformed identity retains the debug representation of the original
/// optional wire value and orders it lexicographically.
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

/// Best-route and ECMP hints do not affect prefix identity or ordering.
///
/// The initial group width is one; annotation counts best routes sharing
/// the same prefix.
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

/// Best routes with the same prefix share one ECMP width across sources.
///
/// Non-best entries keep their initial width of one. A single best route
/// also remains unmarked.
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

/// Unknown source values use the default source's lowercase display name.
pub fn route_source_name(value: i32) -> String {
    let source = RouteSourceId::try_from(value).unwrap_or_default();

    serde_with::short_name(source.as_str_name(), "ROUTE_SOURCE_ID_").to_lowercase()
}

#[cfg(test)]
mod test {
    use commonpb::pb::IpPrefix;

    use super::*;

    /// Creates a minimal row with zero metrics, identifiers set to one and an
    /// initial ECMP width of one.
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

    /// Creates a row from a valid CIDR prefix and an initial ECMP width of one.
    fn make_entry(prefix_str: &str, source: &str, is_best: bool) -> RouteEntry {
        let prefix = Contiguous::<IpNetwork>::parse(prefix_str).expect("must be valid prefix");
        entry_with(PrefixValue::Parsed(prefix), source, is_best)
    }

    /// Creates a row whose malformed identity is the supplied raw string.
    fn make_malformed_entry(prefix_str: &str, source: &str, is_best: bool) -> RouteEntry {
        entry_with(PrefixValue::Malformed(prefix_str.to_string()), source, is_best)
    }

    /// Verifies that missing, incomplete and overlong wire prefixes retain
    /// a malformed identity and a printable marker.
    #[test]
    fn test_route_entry_from_malformed_prefix_does_not_panic() {
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

    #[test]
    fn test_route_entry_malformed_prefixes_stay_distinct() {
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

    /// Verifies that non-best routes do not increase a prefix's ECMP width.
    #[test]
    fn test_annotate_ecmp_groups_marks_multi_best_prefixes() {
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

    #[test]
    fn test_annotate_ecmp_groups_different_sources_are_grouped() {
        let mut entries = vec![
            make_entry("10.0.0.0/8", "static", true),
            make_entry("10.0.0.0/8", "bird", true),
        ];

        annotate_ecmp_groups(&mut entries);

        assert_eq!(2, entries[0].prefix.2);
        assert_eq!(2, entries[1].prefix.2);
    }

    #[test]
    fn test_annotate_ecmp_groups_same_source_grouped() {
        let mut entries = vec![
            make_entry("10.0.0.0/8", "bird", true),
            make_entry("10.0.0.0/8", "bird", true),
        ];

        annotate_ecmp_groups(&mut entries);

        assert_eq!(2, entries[0].prefix.2);
        assert_eq!(2, entries[1].prefix.2);
    }

    #[test]
    fn test_annotate_ecmp_groups_keeps_distinct_malformed_prefixes_apart() {
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
}
