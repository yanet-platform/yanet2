use std::path::PathBuf;

use clap::Parser;
use clap_complete::engine::ArgValueCandidates;

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
    /// Show per-rule ACL counters
    RuleCounters(RuleCountersCmd),
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
    /// Destination MAC address of emitted state-sync frames
    /// (e.g., "00:11:22:33:44:55")
    #[arg(long)]
    pub dst_ether: Option<String>,
    /// Multicast IPv6 destination of emitted state-sync frames
    /// (e.g., "ff02::1")
    #[arg(long)]
    pub dst_addr_multicast: Option<String>,
    /// Multicast port of emitted state-sync frames
    #[arg(long)]
    pub port_multicast: Option<u32>,
    /// Unicast IPv6 destination of emitted state-sync frames
    /// (e.g., "2001:db8::2")
    #[arg(long)]
    pub dst_addr_unicast: Option<String>,
    /// Unicast port of emitted state-sync frames
    #[arg(long)]
    pub port_unicast: Option<u32>,
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

#[derive(Debug, Clone, Parser, Default)]
pub struct RuleCountersCmd {
    /// ACL config name; omit to show rule counters of every config
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: Option<String>,
}
