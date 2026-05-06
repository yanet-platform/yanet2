use commonpb::pb::MacAddress;
use tabled::Tabled;

#[allow(clippy::all, non_snake_case)]
pub mod routepb {
    tonic::include_proto!("routepb");
}

fn format_mac(mac: Option<MacAddress>) -> String {
    let bytes = mac.map(|m| m.addr.to_be_bytes()).unwrap_or_default();
    format!(
        "{:02x}:{:02x}:{:02x}:{:02x}:{:02x}:{:02x}",
        bytes[2], bytes[3], bytes[4], bytes[5], bytes[6], bytes[7],
    )
}

/// FIB entry for display in the CLI table.
#[derive(Debug, Tabled)]
pub struct FibDisplayEntry {
    #[tabled(rename = "Prefix")]
    pub prefix: String,
    #[tabled(rename = "Dst MAC")]
    pub dst_mac: String,
    #[tabled(rename = "Src MAC")]
    pub src_mac: String,
    #[tabled(rename = "Device")]
    pub device: String,
}

impl FibDisplayEntry {
    /// Convert a FIBEntry proto message into one or more display
    /// entries (one per nexthop for ECMP).
    pub fn from_fib_entry(entry: routepb::FibEntry) -> Vec<Self> {
        let prefix = entry.prefix;
        entry
            .nexthops
            .into_iter()
            .map(|nh| FibDisplayEntry {
                prefix: prefix.clone(),
                dst_mac: format_mac(nh.dst_mac),
                src_mac: format_mac(nh.src_mac),
                device: nh.device,
            })
            .collect()
    }
}
