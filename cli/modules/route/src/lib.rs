use core::fmt::{self, Display, Formatter};

use colored::Colorize;
use ipnet::IpNet;
use tabled::Tabled;

/// BGP Large Community value.
#[derive(Debug)]
pub struct LargeCommunity {
    pub global_administrator: u32,
    pub local_data_part1: u32,
    pub local_data_part2: u32,
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
