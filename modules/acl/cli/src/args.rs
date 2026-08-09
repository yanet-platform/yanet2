use core::time::Duration;
use std::path::PathBuf;

use clap::Parser;
use clap_complete::engine::ArgValueCandidates;

/// Parse duration from string (e.g., "60s", "5m", "1h")
fn parse_duration(s: &str) -> Result<Duration, String> {
    humantime::parse_duration(s).map_err(|e| e.to_string())
}

#[allow(clippy::large_enum_variant)]
#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List all ACL configs
    List,
    /// Delete an ACL config
    Delete(DeleteCmd),
    /// Upload a new ACL config from a YAML file
    Update(UpdateCmd),
    /// Show ACL config rules
    Show(ShowCmd),
    /// Show ACL metrics
    Metrics(MetricsCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// ACL config name
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// ACL config name
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,
    /// Path to the ruleset YAML file
    #[arg(required = true, long = "rules", value_name = "PATH")]
    pub rules: PathBuf,

    /// Name of the standalone fwstate-map (kind V4) this config borrows from.
    ///
    /// Allowed for any stateful ruleset (CREATE_STATE or CHECK_STATE);
    /// required when the ruleset uses ACTION_KIND_CREATE_STATE.
    #[arg(long = "map-name-v4", value_name = "NAME")]
    pub map_name_v4: Option<String>,

    /// Name of the standalone fwstate-map (kind V6) this config borrows from.
    ///
    /// Same requirement contract as --map-name-v4.
    #[arg(long = "map-name-v6", value_name = "NAME")]
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

impl UpdateCmd {
    /// Returns true when any synchronization flag is set.
    ///
    /// Drives whether a SyncConfig is built for the update request.
    pub fn has_sync_flags(&self) -> bool {
        self.src_addr.is_some()
            || self.dst_ether.is_some()
            || self.dst_addr_multicast.is_some()
            || self.port_multicast.is_some()
            || self.dst_addr_unicast.is_some()
            || self.port_unicast.is_some()
            || self.tcp_syn_ack.is_some()
            || self.tcp_syn.is_some()
            || self.tcp_fin.is_some()
            || self.tcp.is_some()
            || self.udp.is_some()
            || self.default.is_some()
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// ACL config name
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, clap::ValueEnum)]
pub enum MetricName {
    /// Packet counters (acl_*_packets)
    Packets,
    /// Byte counters (acl_*_bytes)
    Bytes,
    /// Action outcome counters: allow, deny, count, check_state, create_state,
    /// unknown
    Action,
    /// State-table counters: check_state, create_state, state_miss
    State,
    /// Per-rule named counters (acl_rule_*)
    Rule,
    /// Compiled filter rule counts per protocol
    FilterRuleCount,
    /// Compilation time and memory usage
    Compilation,
    /// gRPC handler call latency histograms
    Handler,
}

impl MetricName {
    pub fn as_filter(&self) -> &'static str {
        match self {
            Self::Packets => "packets",
            Self::Bytes => "bytes",
            Self::Action => "action",
            Self::State => "state",
            Self::Rule => "rule",
            Self::FilterRuleCount => "filter_rule_count",
            Self::Compilation => "compilation",
            Self::Handler => "handler",
        }
    }
}

#[derive(Debug, Clone, Parser, Default)]
pub struct MetricsCmd {
    /// Server-side tag filter, e.g. --tag config=my-acl --tag device=eth0.
    ///
    /// An empty value requires the label to be absent, `*` requires it to be
    /// present with any value, and any other value requires an exact match.
    /// Mirrors the counters `CounterTag` semantics.
    #[arg(long = "tag", short = 't', value_name = "NAME=VALUE")]
    pub tags: Vec<String>,
    /// Show only metrics matching this category
    #[arg(long, short, value_enum)]
    pub name: Option<MetricName>,
}
