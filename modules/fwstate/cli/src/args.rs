use core::{net::Ipv6Addr, time::Duration};

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
    /// Show fwstate metrics
    Metrics(MetricsCmd),
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

    /// Name of the published fwstate-map (kind V4) object to link
    #[arg(long)]
    pub map_name_v4: Option<String>,

    /// Name of the published fwstate-map (kind V6) object to link
    #[arg(long)]
    pub map_name_v6: Option<String>,

    /// Source IPv6 address (e.g., "2001:db8::1")
    #[arg(long)]
    pub src_addr: Option<Ipv6Addr>,

    /// Multicast IPv6 address (e.g., "ff02::1")
    #[arg(long)]
    pub dst_addr_multicast: Option<Ipv6Addr>,

    /// Multicast port
    #[arg(long)]
    pub port_multicast: Option<u16>,

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
    /// Omitted or zero keeps the currently configured window.
    #[arg(long, value_parser = parse_duration)]
    pub sync_suppress_timeout: Option<Duration>,
}

#[derive(Debug, Clone, clap::ValueEnum)]
pub enum MetricName {
    /// Dataplane packet/byte counters (fwstate_*_packets, fwstate_*_bytes)
    Counters,
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
            Self::Sync => kind == Kind::Counter && m.name.starts_with("fwstate_sync_"),
            Self::Grpc => m.name.starts_with("grpc_"),
        }
    }
}

#[derive(Debug, Clone, Parser, Default)]
pub struct MetricsCmd {
    /// Label filter, e.g. --label config=my-fwstate
    #[arg(long = "label", short = 'l', value_name = "KEY=VALUE")]
    pub labels: Vec<String>,
    /// Show only metrics matching this category
    #[arg(long, short, value_enum)]
    pub name: Option<MetricName>,
}
