use core::{
    fmt::{self, Display, Formatter},
    str::FromStr,
};

use code::RouteSourceId;
use colored::Colorize;
use ipnet::IpNet;
use tabled::Tabled;

#[allow(non_snake_case)]
pub mod code {
    tonic::include_proto!("routepb");
}

/// BGP Large Community value.
#[derive(Debug)]
pub struct LargeCommunity {
    pub global_administrator: u32,
    pub local_data_part1: u32,
    pub local_data_part2: u32,
}

impl From<code::LargeCommunity> for LargeCommunity {
    fn from(community: code::LargeCommunity) -> Self {
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

/// List of BGP Large Communities.
#[derive(Debug)]
pub struct Communities(pub Vec<LargeCommunity>);

impl Display for Communities {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let communities: Vec<String> = self.0.iter().map(|c| c.to_string()).collect();
        write!(f, "{}", communities.join(" "))
    }
}

/// Route entry in the routing table.
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

impl From<code::Route> for RouteEntry {
    fn from(route: code::Route) -> Self {
        let communities = route.large_communities.into_iter().map(|c| c.into()).collect();

        // TODO: migrate to strongly-typed protobuf messages for IPNetwork.
        let prefix = IpNet::from_str(&route.prefix).expect("must be valid prefix");

        let source = RouteSourceId::try_from(route.source)
            .unwrap_or_default()
            .as_str_name()
            .strip_prefix("ROUTE_SOURCE_ID_")
            .unwrap_or_default()
            .to_lowercase();

        Self {
            prefix: Prefix(prefix, route.is_best),
            next_hop: route.next_hop,
            peer: route.peer,
            source,
            peer_as: route.peer_as,
            origin_as: route.origin_as,
            pref: route.pref,
            med: route.med,
            communities: Communities(communities),
        }
    }
}

#[derive(Debug)]
pub struct Prefix(pub IpNet, pub bool);

impl Display for Prefix {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let Prefix(ref prefix, is_best) = self;
        let prefix = prefix.to_string();
        let prefix = if *is_best {
            prefix.into()
        } else {
            prefix.truecolor(127, 127, 127)
        };

        write!(f, "{prefix}")
    }
}
