use core::{net::Ipv6Addr, time::Duration};

use clap::Parser;
use clap_complete::engine::ArgValueCandidates;

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
