use clap::Parser;
use std::time::Duration;

/// Parse duration from string (e.g., "60s", "5m", "1h")
fn parse_duration(s: &str) -> Result<Duration, String> {
    humantime::parse_duration(s).map_err(|e| e.to_string())
}

#[allow(clippy::large_enum_variant)]
#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    List,
    Delete(DeleteCmd),
    Update(UpdateCmd),
    Show(ShowCmd),
    /// Set fwstate configuration (map and sync settings)
    SetFwstateConfig(SetFwstateConfigCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteCmd {
    /// The name of the module to delete
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Dataplane instances from which to delete config
    #[arg(long, short, required = true)]
    pub instance: u32,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the module to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
    /// Dataplane instances where the changes should be applied.
    #[arg(long, short, required = true)]
    pub instance: u32,
    /// Ruleset file name.
    #[arg(required = true, long = "rules", value_name = "rules")]
    pub rules: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// ACL module name to operate on.
    #[arg(long = "cfg", short)]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct SetFwstateConfigCmd {
    /// ACL module name to operate on.
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
