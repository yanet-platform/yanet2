use core::{
    fmt::{self, Display, Formatter},
    net::IpAddr,
};
use std::time::SystemTime;

use netip::MacAddr;
use tabled::Tabled;

// Newtype wrapper around NeighbourState for better display
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub struct State(pub i32);

impl Display for State {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let v = match self {
            Self(0x00) => "NONE",
            Self(0x01) => "INCOMPLETE",
            Self(0x02) => "REACHABLE",
            Self(0x04) => "STALE",
            Self(0x08) => "DELAY",
            Self(0x10) => "PROBE",
            Self(0x20) => "FAILED",
            Self(0x40) => "NOARP",
            Self(0x80) => "PERMANENT",
            Self(..) => "UNKNOWN",
        };

        write!(f, "{v}")
    }
}

/// Represents the time since a neighbor entry was last updated.
///
/// This is a newtype wrapper around SystemTime that provides custom
/// display formatting to show the age as a human-readable duration.
#[derive(Debug)]
pub struct Age(pub SystemTime);

impl Display for Age {
    fn fmt(&self, f: &mut Formatter) -> Result<(), fmt::Error> {
        let now = SystemTime::now();
        let duration = match self {
            Self(timestamp) => now.duration_since(*timestamp).unwrap_or_default(),
        };

        write!(f, "{:.2?}", duration)
    }
}

/// Represents a neighbor entry in the routing table.
#[derive(Debug, Tabled)]
pub struct NeighbourEntry {
    /// IP address of the next-hop router or directly connected host.
    #[tabled(rename = "NEXTHOP")]
    pub next_hop: IpAddr,
    /// MAC address of the next-hop device.
    #[tabled(rename = "NEIGHBOUR MAC")]
    pub link_addr: MacAddr,
    /// MAC address of the local interface that connects to this neighbor.
    #[tabled(rename = "INTERFACE MAC")]
    pub hardware_addr: MacAddr,
    /// Current state of the neighbor relationship (e.g., REACHABLE, STALE,
    /// PROBE).
    #[tabled(rename = "STATE")]
    pub state: State,
    /// Time elapsed since this neighbor entry was last updated.
    #[tabled(rename = "AGE")]
    pub age: Age,
}
