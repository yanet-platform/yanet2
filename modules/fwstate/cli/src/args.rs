use core::{
    net::{Ipv6Addr, SocketAddrV6},
    time::Duration,
};

use clap::Parser;
use clap_complete::engine::ArgValueCandidates;

/// Parse duration from string (e.g., "60s", "5m", "1h")
fn parse_duration(s: &str) -> Result<Duration, String> {
    humantime::parse_duration(s).map_err(|e| e.to_string())
}

/// One synchronization endpoint: the source stamped on emitted packets, the
/// destination, and the destination port.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SyncEndpoint {
    pub src: Ipv6Addr,
    pub dst: Ipv6Addr,
    pub port: u16,
}

/// Parse "SRC@DST:PORT"; DST:PORT accepts both "[dst]:port" and "dst:port".
pub(crate) fn parse_sync_endpoint(value: &str) -> Result<SyncEndpoint, String> {
    let (src, rest) = value.split_once('@').ok_or("endpoint must be SRC@DST:PORT")?;
    let src: Ipv6Addr = src.parse().map_err(|err| format!("invalid source address: {err}"))?;

    let (dst, port) = match rest.parse::<SocketAddrV6>() {
        Ok(endpoint) => {
            if endpoint.scope_id() != 0 {
                return Err("IPv6 scope IDs are not supported".to_string());
            }
            (*endpoint.ip(), endpoint.port())
        }
        Err(_) => {
            let (dst, port) = rest.rsplit_once(':').ok_or("endpoint must name a port")?;
            let dst: Ipv6Addr = dst
                .parse()
                .map_err(|err| format!("invalid destination address: {err}"))?;
            let port: u16 = port
                .parse()
                .map_err(|_| "endpoint port must be in 1..=65535".to_string())?;
            (dst, port)
        }
    };
    if port == 0 {
        return Err("endpoint port must be in 1..=65535".to_string());
    }
    Ok(SyncEndpoint { src, dst, port })
}

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

    /// Synchronization endpoint (e.g., "fd00::1@ff02::1:9999"): packets are
    /// emitted from the source toward the destination port. Repeatable; an
    /// update carrying endpoints replaces the whole list. Named
    /// --sync-endpoint because the shared global --endpoint names the
    /// gateway.
    #[arg(long = "sync-endpoint", value_name = "SRC@DST:PORT", value_parser = parse_sync_endpoint)]
    pub sync_endpoint: Vec<SyncEndpoint>,

    /// Remove every synchronization endpoint.
    #[arg(long, conflicts_with = "sync_endpoint")]
    pub no_sync_endpoints: bool,

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

    /// Largest emitted sync packet in bytes, IPv6 and UDP headers included;
    /// it bounds how many frames one packet batches, as does the mbuf data
    /// room. Zero selects 1500; a nonzero value must be at least 104.
    #[arg(long)]
    pub sync_mtu: Option<u16>,
}
