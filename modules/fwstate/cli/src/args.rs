use core::{
    net::{Ipv6Addr, SocketAddrV6},
    time::Duration,
};

use clap::Parser;
use clap_complete::engine::ArgValueCandidates;
use netip::MacAddr;

/// Parse duration from string (e.g., "60s", "5m", "1h")
fn parse_duration(s: &str) -> Result<Duration, String> {
    humantime::parse_duration(s).map_err(|e| e.to_string())
}

fn parse_sync_endpoint(value: &str) -> Result<SocketAddrV6, String> {
    let endpoint = value.parse::<SocketAddrV6>().map_err(|err| err.to_string())?;
    if endpoint.port() == 0 {
        return Err("endpoint port must be in 1..=65535".to_string());
    }
    if endpoint.scope_id() != 0 {
        return Err("IPv6 scope IDs are not supported".to_string());
    }
    Ok(endpoint)
}

#[allow(clippy::large_enum_variant)]
#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// List all fwstate configurations.
    List,
    /// Delete a fwstate configuration.
    Delete(DeleteCmd),
    /// Create or update only the supplied map and sync settings.
    Update(UpdateCmd),
    /// Show fwstate configuration.
    Show(ShowCmd),
}

impl ModeCmd {
    pub(crate) fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Delete(..) => "delete",
            Self::Update(..) => "update",
            Self::Show(..) => "show",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the fwstate config to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// FWState config name to show.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// FWState config name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(crate::config_candidates))]
    pub config_name: String,

    /// Name of the published V4 fwstate-map to link; empty unlinks it.
    #[arg(long)]
    pub map_name_v4: Option<String>,

    /// Name of the published V6 fwstate-map to link; empty unlinks it.
    #[arg(long)]
    pub map_name_v6: Option<String>,

    /// Source IPv6 address (e.g., "2001:db8::1").
    #[arg(long)]
    pub src_addr: Option<Ipv6Addr>,

    /// Destination MAC address (e.g., "00:11:22:33:44:55").
    #[arg(long)]
    pub dst_ether: Option<MacAddr>,

    /// Multicast synchronization endpoint (e.g., "[ff02::1]:9999").
    #[arg(
        long,
        value_name = "[ADDR]:PORT",
        value_parser = parse_sync_endpoint,
        conflicts_with_all = ["dst_addr_multicast", "port_multicast", "no_multicast"]
    )]
    pub multicast: Option<SocketAddrV6>,

    /// Unicast synchronization endpoint (e.g., "[2001:db8::2]:9999").
    #[arg(
        long,
        value_name = "[ADDR]:PORT",
        value_parser = parse_sync_endpoint,
        conflicts_with_all = ["dst_addr_unicast", "port_unicast", "no_unicast"]
    )]
    pub unicast: Option<SocketAddrV6>,

    /// Deprecated: use --multicast instead.
    #[arg(long)]
    pub dst_addr_multicast: Option<Ipv6Addr>,

    /// Deprecated: use --multicast instead.
    #[arg(long)]
    pub port_multicast: Option<u16>,

    /// Deprecated: use --unicast instead.
    #[arg(long)]
    pub dst_addr_unicast: Option<Ipv6Addr>,

    /// Deprecated: use --unicast instead.
    #[arg(long)]
    pub port_unicast: Option<u16>,

    /// Remove the multicast synchronization endpoint.
    #[arg(
        long,
        conflicts_with_all = ["multicast", "dst_addr_multicast", "port_multicast"]
    )]
    pub no_multicast: bool,

    /// Remove the unicast synchronization endpoint.
    #[arg(long, conflicts_with_all = ["unicast", "dst_addr_unicast", "port_unicast"])]
    pub no_unicast: bool,

    /// TCP SYN-ACK timeout (e.g., "60s", "5m", "1h").
    #[arg(long, value_parser = parse_duration)]
    pub tcp_syn_ack: Option<Duration>,

    /// TCP SYN timeout (e.g., "60s", "5m", "1h").
    #[arg(long, value_parser = parse_duration)]
    pub tcp_syn: Option<Duration>,

    /// TCP FIN timeout (e.g., "60s", "5m", "1h").
    #[arg(long, value_parser = parse_duration)]
    pub tcp_fin: Option<Duration>,

    /// TCP established timeout (e.g., "60s", "5m", "1h").
    #[arg(long, value_parser = parse_duration)]
    pub tcp: Option<Duration>,

    /// UDP timeout (e.g., "60s", "5m", "1h").
    #[arg(long, value_parser = parse_duration)]
    pub udp: Option<Duration>,

    /// Default timeout (e.g., "60s", "5m", "1h").
    #[arg(long, value_parser = parse_duration)]
    pub default: Option<Duration>,

    /// Sync suppression window: skip redundant state-sync refreshes whose
    /// new expiry lands within this window of the current one (e.g., "8s").
    ///
    /// Omitted keeps the current window; zero disables suppression.
    #[arg(long, value_parser = parse_duration)]
    pub sync_suppress_timeout: Option<Duration>,
}
