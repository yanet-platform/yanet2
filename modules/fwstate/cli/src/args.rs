use core::time::Duration;

use clap::Parser;
use clap_complete::engine::ArgValueCandidates;
use commonpb::pb::Metric;
use ync::metrics::{self, Kind};

/// Parse duration from string (e.g., "60s", "5m", "1h")
fn parse_duration(s: &str) -> Result<Duration, String> {
    humantime::parse_duration(s).map_err(|e| e.to_string())
}

#[allow(clippy::large_enum_variant)]
#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List all fwstate configurations
    List,
    /// Delete a fwstate configuration
    Delete(DeleteCmd),
    /// Update fwstate configuration (map and sync settings)
    Update(UpdateCmd),
    /// Show fwstate configuration
    Show(ShowCmd),
    /// Link fwstate configuration to ACL configurations
    Link(LinkCmd),
    /// Get statistics for fwstate maps
    Stats(StatsCmd),
    /// List entries from fwstate map
    Entries(EntriesCmd),
    /// Show fwstate metrics
    Metrics(MetricsCmd),
    /// Manage standalone fwstate-map objects
    Map {
        #[clap(subcommand)]
        command: MapCmd,
    },
}

/// Address family of a fwstate-map object.
#[derive(Debug, Clone, Copy, PartialEq, Eq, clap::ValueEnum)]
pub enum MapKind {
    /// IPv4 state table
    V4,
    /// IPv6 state table
    V6,
}

#[derive(Debug, Clone, clap::Subcommand)]
pub enum MapCmd {
    /// Create a named fwstate-map and publish it
    Create(MapCreateCmd),
    /// Delete a named fwstate-map
    Delete(MapDeleteCmd),
    /// List registered fwstate-map objects
    List(MapListCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct MapCreateCmd {
    /// Name of the fwstate-map to create
    #[arg(long = "name", short = 'n')]
    pub map_name: String,

    /// Address family of the state table this map owns
    #[arg(long)]
    pub kind: MapKind,

    /// Size of the hash table index (0 uses the service default)
    #[arg(long)]
    pub index_size: Option<u32>,

    /// Number of extra collision buckets (0 uses the service default)
    #[arg(long)]
    pub extra_bucket_count: Option<u32>,

    /// Per-worker state sizing (0 derives the dataplane worker count)
    #[arg(long)]
    pub worker_count: Option<u32>,
}

#[derive(Debug, Clone, Parser)]
pub struct MapDeleteCmd {
    /// Name of the fwstate-map to delete
    #[arg(long = "name", short = 'n')]
    pub map_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct MapListCmd {}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the fwstate config to delete
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// FWState config name to show
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct LinkCmd {
    /// FWState config name to link
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,

    /// ACL config names to link (can be specified multiple times)
    #[arg(long = "acl", required = true, num_args = 1..)]
    pub acl_configs: Vec<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct StatsCmd {
    /// FWState config name to get statistics for
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// FWState config name to operate on
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,

    /// Size of the hash table index for firewall state maps
    #[arg(long)]
    pub index_size: Option<u32>,

    /// Number of extra buckets for collision handling
    #[arg(long)]
    pub extra_bucket_count: Option<u32>,

    /// Source IPv6 address (e.g., "2001:db8::1")
    #[arg(long)]
    pub src_addr: Option<String>,

    /// Multicast IPv6 address (e.g., "ff02::1")
    #[arg(long)]
    pub dst_addr_multicast: Option<String>,

    /// Multicast port
    #[arg(long)]
    pub port_multicast: Option<u32>,

    /// TCP SYN-ACK timeout (e.g., "60s", "5m", "1h")
    #[arg(long, value_parser = parse_duration)]
    pub tcp_syn_ack: Option<Duration>,

    /// TCP SYN timeout (e.g., "60s", "5m", "1h")
    #[arg(long, value_parser = parse_duration)]
    pub tcp_syn: Option<Duration>,

    /// TCP FIN timeout (e.g., "60s", "5m", "1h")
    #[arg(long, value_parser = parse_duration)]
    pub tcp_fin: Option<Duration>,

    /// TCP established timeout (e.g., "60s", "5m", "1h")
    #[arg(long, value_parser = parse_duration)]
    pub tcp: Option<Duration>,

    /// UDP timeout (e.g., "60s", "5m", "1h")
    #[arg(long, value_parser = parse_duration)]
    pub udp: Option<Duration>,

    /// Default timeout (e.g., "60s", "5m", "1h")
    #[arg(long, value_parser = parse_duration)]
    pub default: Option<Duration>,

    /// Sync suppression window: skip redundant state-sync refreshes whose
    /// new expiry lands within this window of the current one (e.g., "8s").
    /// "0s" disables suppression.
    #[arg(long, value_parser = parse_duration)]
    pub sync_suppress_timeout: Option<Duration>,
}

#[derive(Debug, Clone, clap::ValueEnum)]
pub enum DirectionArg {
    Forward,
    Backward,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, clap::ValueEnum)]
pub enum Family {
    /// fw4state map
    Ipv4,
    /// fw6state map
    Ipv6,
}

impl Family {
    /// Renders the family as the request's `is_ipv6` map selector.
    pub fn is_ipv6(self) -> bool {
        matches!(self, Self::Ipv6)
    }
}

#[derive(Debug, Clone, Parser)]
pub struct EntriesCmd {
    /// FWState config name
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,

    /// Address families to list, in the given order
    ///
    /// Repeatable and comma-separated. Omit every family argument to list
    /// IPv4 then IPv6.
    #[arg(
        long = "family",
        short = 'f',
        value_name = "FAMILY",
        value_enum,
        value_delimiter = ','
    )]
    pub families: Vec<Family>,

    /// Shorthand for --family ipv4
    #[arg(long, short = '4', conflicts_with = "families")]
    pub ipv4: bool,

    /// Shorthand for --family ipv6
    #[arg(long, short = '6', conflicts_with = "families")]
    pub ipv6: bool,

    /// Layer index to iterate (0 = active layer)
    #[arg(long, default_value = "0")]
    pub layer: u32,

    /// Include expired entries
    #[arg(long)]
    pub include_expired: bool,

    /// Max entries per gRPC batch
    #[arg(long, default_value = "128")]
    pub batch: u32,

    /// Total number of entries to return (0 = unlimited)
    ///
    /// This limit is shared across both maps when listing both families.
    #[arg(long, default_value = "0")]
    pub count: u32,

    /// Iteration direction
    ///
    /// When listing both families, this flag is applied to each map separately.
    #[arg(long, default_value = "forward")]
    pub direction: DirectionArg,

    /// Starting cursor position (0 = beginning)
    ///
    /// When listing both families, this flag is applied to each map separately.
    /// The second pass starts from this index again, not where the first map
    /// stopped.
    #[arg(long, default_value = "0")]
    pub index: u32,
}

impl EntriesCmd {
    /// Returns the maps to list, in listing order.
    ///
    /// A repeated family collapses onto its first occurrence, so `--family
    /// ipv4,ipv4` reads one map rather than dumping it twice. The shorthand
    /// flags conflict with `--family`, so only one of the two sources is
    /// ever populated.
    pub fn families(&self) -> Vec<Family> {
        if !self.families.is_empty() {
            let mut families = Vec::with_capacity(self.families.len());
            for family in self.families.iter().copied() {
                if !families.contains(&family) {
                    families.push(family);
                }
            }
            return families;
        }

        match (self.ipv4, self.ipv6) {
            (true, false) => vec![Family::Ipv4],
            (false, true) => vec![Family::Ipv6],
            _ => vec![Family::Ipv4, Family::Ipv6],
        }
    }
}

#[derive(Debug, Clone, clap::ValueEnum)]
pub enum MetricName {
    /// Dataplane packet/byte counters (fwstate_*_packets, fwstate_*_bytes)
    Counters,
    /// Map statistics gauges: index_size, total_elements, memory_bytes, etc.
    MapStats,
    /// Sync-related counters (fwstate_sync_*)
    Sync,
    /// gRPC server metrics: call counts and handling latency histograms
    Grpc,
}

impl MetricName {
    /// Returns true when the metric belongs to this category.
    pub fn matches(&self, m: &Metric) -> bool {
        let kind = metrics::proto_kind(m);
        match self {
            Self::Counters => kind == Kind::Counter && m.name.starts_with("fwstate_"),
            Self::MapStats => kind == Kind::Gauge && m.name.starts_with("fwstate_"),
            Self::Sync => kind == Kind::Counter && m.name.starts_with("fwstate_sync_"),
            Self::Grpc => m.name.starts_with("grpc_"),
        }
    }
}

#[derive(Debug, Clone, Parser, Default)]
pub struct MetricsCmd {
    /// Label filter, e.g. --label config=my-fwstate --label af=ipv4
    #[arg(long = "label", short = 'l', value_name = "KEY=VALUE")]
    pub labels: Vec<String>,
    /// Show only metrics matching this category
    #[arg(long, short, value_enum)]
    pub name: Option<MetricName>,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn families_from_args() {
        let both = &[Family::Ipv4, Family::Ipv6][..];
        let cases: [(&[&str], &[Family]); 8] = [
            (&["entries", "--name", "x"], both),
            (&["entries", "--name", "x", "--ipv4"], &[Family::Ipv4]),
            (&["entries", "--name", "x", "--ipv6"], &[Family::Ipv6]),
            (&["entries", "--name", "x", "--ipv4", "--ipv6"], both),
            (&["entries", "--name", "x", "--family", "ipv6"], &[Family::Ipv6]),
            (&["entries", "--name", "x", "-f", "ipv4,ipv6"], both),
            // The listing follows the requested order, and a repeat reads
            // the map once rather than dumping it twice.
            (
                &["entries", "--name", "x", "-f", "ipv6", "-f", "ipv4"],
                &[Family::Ipv6, Family::Ipv4],
            ),
            (&["entries", "--name", "x", "-f", "ipv4,ipv4"], &[Family::Ipv4]),
        ];
        for (args, want) in cases {
            let cmd = EntriesCmd::try_parse_from(args).unwrap();
            assert_eq!(want, cmd.families(), "{args:?}");
        }
    }

    #[test]
    fn shorthand_conflicts_with_family() {
        let args = ["entries", "--name", "x", "--family", "ipv6", "--ipv4"];
        assert!(EntriesCmd::try_parse_from(args).is_err());
    }
}
