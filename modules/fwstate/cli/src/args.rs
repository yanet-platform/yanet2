use std::time::Duration;

use clap::Parser;

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
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the fwstate config to delete
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// FWState config name to show
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct LinkCmd {
    /// FWState config name to link
    #[arg(long = "cfg", short)]
    pub config_name: String,

    /// ACL config names to link (can be specified multiple times)
    #[arg(long = "acl", required = true, num_args = 1..)]
    pub acl_configs: Vec<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct StatsCmd {
    /// FWState config name to get statistics for
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// FWState config name to operate on
    #[arg(long = "cfg", short)]
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
