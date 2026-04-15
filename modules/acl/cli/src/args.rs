use std::path::PathBuf;

use clap::Parser;

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
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// ACL config name
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Path to the ruleset YAML file
    #[arg(required = true, long = "rules", value_name = "PATH")]
    pub rules: PathBuf,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// ACL config name
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, clap::ValueEnum, PartialEq, Default)]
pub enum OutputFormat {
    #[default]
    Json,
    /// Human-readable grouped tables
    Table,
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
    /// Output format
    #[arg(long, short, value_enum, default_value = "json")]
    pub format: OutputFormat,
    /// Label filter, e.g. --label config=my-acl --label device=eth0
    #[arg(long = "label", short = 'l', value_name = "KEY=VALUE")]
    pub labels: Vec<String>,
    /// Show only metrics matching this category
    #[arg(long, short, value_enum)]
    pub name: Option<MetricName>,
}
