//! JSON payload for `fib show --format json`.
//!
//! Each entry carries `start`/`end` -- an absent or malformed endpoint
//! renders as the literal `"invalid"`, matching the human view -- plus
//! `nexthops`, always an array mirroring the wire entry, empty rather
//! than absent for an entry with none.

use serde::Serialize;

use super::{format_mac, range_endpoints};
use crate::routepb::{FibEntry, FibNexthop};

/// One nexthop, as serialized in a [`FibEntryJson`].
#[derive(Debug, Serialize)]
pub struct FibNexthopJson {
    pub dst_mac: String,
    pub src_mac: String,
    pub device: String,
}

impl From<&FibNexthop> for FibNexthopJson {
    fn from(nexthop: &FibNexthop) -> Self {
        Self {
            dst_mac: format_mac(nexthop.dst_mac),
            src_mac: format_mac(nexthop.src_mac),
            device: nexthop.device.clone(),
        }
    }
}

/// One FIB entry, as serialized for `--format json`.
#[derive(Debug, Serialize)]
pub struct FibEntryJson {
    pub start: String,
    pub end: String,
    pub nexthops: Vec<FibNexthopJson>,
}

impl From<&FibEntry> for FibEntryJson {
    fn from(entry: &FibEntry) -> Self {
        let (start, end) = range_endpoints(entry.range.as_ref());
        Self {
            start,
            end,
            nexthops: entry.nexthops.iter().map(FibNexthopJson::from).collect(),
        }
    }
}

#[cfg(test)]
mod test {
    use commonpb::pb::IpRange;

    use super::*;

    /// Pins the serialized shape byte-for-byte: `--format json` is a
    /// contract, and this is what a caller downstream of it parses
    /// against. Renaming, reordering, or nesting a field would change this
    /// string and must fail this test.
    #[test]
    fn entry_json_matches_expected_shape() {
        let entry = FibEntry {
            range: Some(IpRange::from((
                "10.0.0.0".parse().unwrap(),
                "10.0.0.255".parse().unwrap(),
            ))),
            nexthops: vec![FibNexthop {
                dst_mac: None,
                src_mac: None,
                device: "vlan100".to_owned(),
            }],
        };

        let json = serde_json::to_string(&FibEntryJson::from(&entry)).unwrap();

        assert_eq!(
            r#"{"start":"10.0.0.0","end":"10.0.0.255","nexthops":[{"dst_mac":"00:00:00:00:00:00","src_mac":"00:00:00:00:00:00","device":"vlan100"}]}"#,
            json
        );
    }
}
