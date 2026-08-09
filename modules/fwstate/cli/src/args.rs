use core::time::Duration;

use clap::{Parser, Subcommand};
use clap_complete::engine::ArgValueCandidates;
use commonpb::pb::Metric;
use ync::metrics::{self, Kind};

/// Parse duration from string (e.g., "60s", "5m", "1h")
fn parse_duration(s: &str) -> Result<Duration, String> {
    humantime::parse_duration(s).map_err(|e| e.to_string())
}

/// Highest value accepted for `--worker-count`.
///
/// Matches the width of the C-side `uint16` parameter of
/// `fwstate_map_create_maps` (see `maxWorkerCount` in
/// `modules/fwstate/controlplane/map_service.go`). The proto field is a
/// `uint32`, so the bound is enforced here to give an immediate, clear error
/// instead of a round-trip to the server.
const MAX_WORKER_COUNT: u32 = 65535;

/// Parse and validate `worker_count`.
///
/// Rejects zero and values exceeding the C-side `uint16` range, mirroring the
/// server-side `validateWorkerCount` check.
fn parse_worker_count(s: &str) -> Result<u32, String> {
    let value: u32 = s.parse().map_err(|err: core::num::ParseIntError| err.to_string())?;
    if value == 0 {
        return Err("worker_count must be greater than zero".to_string());
    }
    if value > MAX_WORKER_COUNT {
        return Err(format!("worker_count {value} exceeds maximum {MAX_WORKER_COUNT}"));
    }
    Ok(value)
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
    /// List entries from a fwstate-map
    Entries(EntriesCmd),
    /// Show fwstate metrics
    Metrics(MetricsCmd),
    /// Manage standalone named fwstate-map objects
    #[command(subcommand)]
    Map(MapCmd),
}

/// Subcommands for the standalone named fwstate-map object.
#[derive(Debug, Clone, Subcommand)]
pub enum MapCmd {
    /// Create a new named fwstate-map for one address family
    Create(MapCreateCmd),
    /// Delete a named fwstate-map (refuses if still referenced)
    Delete(MapDeleteCmd),
    /// List all named fwstate-map objects
    List,
    /// Get statistics for a named fwstate-map
    Stats(MapStatsCmd),
    /// Insert a new layer into a named fwstate-map's chain
    InsertLayer(MapInsertLayerCmd),
}

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
pub struct UpdateCmd {
    /// FWState config name to operate on
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,

    /// Name of the standalone fwstate-map (kind V4) this config references.
    ///
    /// The config resolves the map by name at publish time. When omitted on
    /// an update the currently referenced v4 map is preserved.
    #[arg(long = "map-name-v4")]
    pub map_name_v4: Option<String>,

    /// Name of the standalone fwstate-map (kind V6) this config references.
    ///
    /// Same semantics as --map-name-v4 for the v6 family.
    #[arg(long = "map-name-v6")]
    pub map_name_v6: Option<String>,

    /// Source IPv6 address (e.g., "2001:db8::1")
    #[arg(long)]
    pub src_addr: Option<String>,

    /// Destination MAC address (e.g., "00:11:22:33:44:55")
    #[arg(long)]
    pub dst_ether: Option<String>,

    /// Multicast IPv6 address (e.g., "ff02::1")
    #[arg(long)]
    pub dst_addr_multicast: Option<String>,

    /// Multicast port
    #[arg(long)]
    pub port_multicast: Option<u32>,

    /// Unicast IPv6 address (e.g., "2001:db8::2")
    #[arg(long)]
    pub dst_addr_unicast: Option<String>,

    /// Unicast port
    #[arg(long)]
    pub port_unicast: Option<u32>,

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
}

#[derive(Debug, Clone, clap::ValueEnum)]
pub enum DirectionArg {
    Forward,
    Backward,
}

/// Address family of a standalone fwstate-map.
#[derive(Debug, Clone, clap::ValueEnum)]
pub enum MapKind {
    V4,
    V6,
}

#[derive(Debug, Clone, Parser)]
pub struct EntriesCmd {
    /// Name of the fwstate-map to iterate
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::map_candidates))]
    pub map_name: String,

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
    #[arg(long, default_value = "0")]
    pub count: u32,

    /// Iteration direction
    #[arg(long, default_value = "forward")]
    pub direction: DirectionArg,

    /// Starting cursor position (0 = beginning)
    #[arg(long, default_value = "0")]
    pub index: u32,
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

#[derive(Debug, Clone, Parser)]
pub struct MapCreateCmd {
    /// Name of the fwstate-map to create
    #[arg(long = "name", short = 'n')]
    pub name: String,

    /// Address family of the map
    #[arg(long, short = 'k', value_enum)]
    pub kind: MapKind,

    /// Size of the hash table index (0 = server default)
    #[arg(long)]
    pub index_size: u32,

    /// Number of extra buckets for collision handling (0 = server default)
    #[arg(long)]
    pub extra_bucket_count: u32,

    /// Number of workers (1..=65535, matches the C-side uint16 range)
    #[arg(long, value_parser = parse_worker_count)]
    pub worker_count: u32,
}

#[derive(Debug, Clone, Parser)]
pub struct MapDeleteCmd {
    /// Name of the fwstate-map to delete
    #[arg(long = "name", short = 'n')]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct MapStatsCmd {
    /// Name of the fwstate-map to get statistics for
    #[arg(long = "name", short = 'n')]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct MapInsertLayerCmd {
    /// Name of the fwstate-map to insert the layer into
    #[arg(long = "name", short = 'n')]
    pub name: String,

    /// Size of the hash table index (0 = server default)
    #[arg(long)]
    pub index_size: u32,

    /// Number of extra buckets for collision handling (0 = server default)
    #[arg(long)]
    pub extra_bucket_count: u32,

    /// Number of workers (1..=65535, matches the C-side uint16 range)
    #[arg(long, value_parser = parse_worker_count)]
    pub worker_count: u32,
}

#[cfg(test)]
mod tests {
    use super::parse_worker_count;

    #[test]
    fn worker_count_accepts_minimum() {
        assert_eq!(1, parse_worker_count("1").unwrap());
    }

    #[test]
    fn worker_count_accepts_maximum() {
        assert_eq!(65535, parse_worker_count("65535").unwrap());
    }

    #[test]
    fn worker_count_rejects_zero() {
        let err = parse_worker_count("0").unwrap_err();
        assert!(err.contains("must be greater than zero"), "unexpected error: {err}");
    }

    #[test]
    fn worker_count_rejects_above_uint16() {
        let err = parse_worker_count("65536").unwrap_err();
        assert!(err.contains("exceeds maximum 65535"), "unexpected error: {err}");
    }

    #[test]
    fn worker_count_rejects_non_numeric() {
        assert!(parse_worker_count("abc").is_err());
    }
}
