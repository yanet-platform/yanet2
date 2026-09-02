use core::net::Ipv6Addr;
use std::path::PathBuf;

use clap::Parser;
use clap_complete::engine::ArgValueCandidates;
use netip::MacAddr;

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
    /// Show per-rule ACL counter metrics
    MetricsRules(MetricsRulesCmd),
    /// Show per-rule ACL counters
    RuleCounters(RuleCountersCmd),
}

impl ModeCmd {
    pub(crate) fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Delete(..) => "delete",
            Self::Update(..) => "update",
            Self::Show(..) => "show",
            Self::MetricsRules(..) => "metrics-rules",
            Self::RuleCounters(..) => "rule-counters",
        }
    }
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
    /// Path to the ruleset YAML file.
    #[arg(value_name = "PATH")]
    pub file: PathBuf,
    /// Name of the standalone fwstate-map (kind V4) whose fwtable this
    /// config uses for state lookups
    #[arg(long = "map-name-v4", value_name = "NAME")]
    pub map_name_v4: Option<String>,
    /// Name of the standalone fwstate-map (kind V6) whose fwtable this
    /// config uses for state lookups
    #[arg(long = "map-name-v6", value_name = "NAME")]
    pub map_name_v6: Option<String>,
    /// Destination MAC address of emitted state-sync frames
    /// (e.g., "00:11:22:33:44:55")
    #[arg(long)]
    pub dst_ether: Option<MacAddr>,
    /// Multicast IPv6 destination of emitted state-sync frames
    /// (e.g., "ff02::1")
    #[arg(long)]
    pub dst_addr_multicast: Option<Ipv6Addr>,
    /// Multicast port of emitted state-sync frames
    #[arg(long)]
    pub port_multicast: Option<u16>,
    /// Unicast IPv6 destination of emitted state-sync frames
    /// (e.g., "2001:db8::2")
    #[arg(long)]
    pub dst_addr_unicast: Option<Ipv6Addr>,
    /// Unicast port of emitted state-sync frames
    #[arg(long)]
    pub port_unicast: Option<u16>,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// ACL config name
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser, Default)]
pub struct MetricsRulesCmd {
    /// ACL config name, omit to match every config.
    #[arg(long = "name", short = 'n', alias = "config", add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: Option<String>,
    /// Dataplane device name; omit to match every device
    #[arg(long = "device", short = 'd')]
    pub device: Option<String>,
    /// Pipeline name; omit to match every pipeline
    #[arg(long = "pipeline", short = 'p')]
    pub pipeline: Option<String>,
    /// Pipeline function name; omit to match every function
    #[arg(long = "function", short = 'f')]
    pub function: Option<String>,
    /// Pipeline chain name; omit to match every chain
    #[arg(long = "chain", short = 'c')]
    pub chain: Option<String>,
}

#[derive(Debug, Clone, Parser, Default)]
pub struct RuleCountersCmd {
    /// ACL config name; omit to show rule counters of every config
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: Option<String>,
}
