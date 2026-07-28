//! Domain model for `fib show`.
//!
//! The wire carries one [`FibRangeEntry`] per dataplane FIB row: a
//! contiguous address range plus its (possibly ECMP) nexthops.
//! [`build_records`] turns each row into a [`FibRecord`] in the order the
//! server sent it. Both the human view ([`render`]) and the JSON payload
//! ([`json`]) are built from those same records, so a record dropped or
//! reshaped here is dropped or reshaped in both.

pub mod json;
pub mod render;

use core::net::IpAddr;

use commonpb::pb::{IpRange, MacAddress};
use netip::MacAddr;

use crate::routepb::{FibNexthop, FibRangeEntry};

/// One dataplane FIB row's range, classified as well-formed or invalid.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RangeUnit {
    Range { start: IpAddr, end: IpAddr },
    Invalid,
}

/// One FIB record: a range paired with the nexthop(s) attached to it.
///
/// `nexthops` always mirrors the wire entry regardless of how `range`
/// classifies -- a [`RangeUnit::Invalid`] record keeps the nexthops the
/// server sent right alongside it, since that is the data an operator
/// needs to diagnose a malformed, absent, or inverted range. A row
/// carrying no nexthops still yields a record, with an empty `nexthops`,
/// so it can never vanish from the output.
#[derive(Debug, Clone)]
pub struct FibRecord {
    range: RangeUnit,
    nexthops: Vec<FibNexthop>,
}

/// Builds one [`FibRecord`] per row in `entries`, preserving wire order.
pub fn build_records(entries: &[FibRangeEntry]) -> Vec<FibRecord> {
    entries.iter().map(build_record).collect()
}

/// Builds one `FibRangeEntry`'s [`FibRecord`].
///
/// `nexthops` is cloned from `entry` unconditionally: the classification of
/// `range` and the fate of the nexthops are independent. `--format json` is
/// the contract for the wire data and must carry whatever nexthops the
/// server attached, even to a malformed, absent, or inverted range -- that
/// is exactly the data an operator needs to diagnose it. The human view is
/// free to summarise such a record away to a single placeholder row; that
/// choice belongs to the renderer, not to this constructor.
fn build_record(entry: &FibRangeEntry) -> FibRecord {
    let range = range_unit(entry.range.as_ref());
    let nexthops = entry.nexthops.clone();

    FibRecord { range, nexthops }
}

/// Classifies a wire `range` as well-formed or invalid.
///
/// A range is invalid when `range` is absent, when converting its two
/// endpoints to an `IpAddr` pair fails -- a missing endpoint, an address
/// byte length that is neither 4 nor 16, or an address-family mismatch
/// between `start` and `end` -- or when it is inverted (`start` after
/// `end`). `IpAddr`'s `Ord` only orders numerically within a single
/// family, so the inversion check runs after the conversion has already
/// established that both endpoints exist and share a family -- checking it
/// first would compare, for instance, an IPv4 `start` against an IPv6
/// `end` and report a meaningless verdict.
fn range_unit(range: Option<&IpRange>) -> RangeUnit {
    let Some(range) = range else {
        return RangeUnit::Invalid;
    };

    let Ok((start, end)) = <(IpAddr, IpAddr)>::try_from(range) else {
        return RangeUnit::Invalid;
    };

    if start > end {
        return RangeUnit::Invalid;
    }

    RangeUnit::Range { start, end }
}

/// Renders a nexthop's MAC address, or a fallback for a missing/invalid one.
fn format_mac(mac: Option<MacAddress>) -> String {
    let mac = match mac {
        Some(mac) => match MacAddr::try_from(&mac) {
            Ok(mac) => return mac.to_string(),
            Err(..) => "invalid",
        },
        None => "00:00:00:00:00:00",
    };
    mac.to_string()
}

#[cfg(test)]
mod test {
    use commonpb::pb::IpAddress;

    use super::*;

    fn ip_range(start: &str, end: &str) -> IpRange {
        IpRange::from((start.parse::<IpAddr>().unwrap(), end.parse::<IpAddr>().unwrap()))
    }

    fn nexthop(device: &str) -> FibNexthop {
        FibNexthop {
            dst_mac: None,
            src_mac: None,
            device: device.to_owned(),
        }
    }

    fn entry(range: Option<IpRange>, nexthops: Vec<FibNexthop>) -> FibRangeEntry {
        FibRangeEntry { range, nexthops }
    }

    #[test]
    fn build_records_yields_one_record_per_entry_in_order() {
        let entries = vec![
            entry(Some(ip_range("10.0.0.0", "10.0.0.255")), vec![nexthop("vlan100")]),
            entry(Some(ip_range("172.16.0.0", "172.16.255.255")), Vec::new()),
        ];

        let records = build_records(&entries);

        assert_eq!(2, records.len());
        assert_eq!(
            RangeUnit::Range {
                start: "10.0.0.0".parse().unwrap(),
                end: "10.0.0.255".parse().unwrap(),
            },
            records[0].range
        );
        assert_eq!(1, records[0].nexthops.len());
        assert!(records[1].nexthops.is_empty());
    }

    #[test]
    fn absent_range_is_invalid() {
        let records = build_records(&[entry(None, vec![nexthop("vlan100")])]);

        assert_eq!(RangeUnit::Invalid, records[0].range);
    }

    #[test]
    fn missing_endpoint_is_invalid() {
        let range = IpRange {
            start: Some(IpAddress::from("10.0.0.1".parse::<IpAddr>().unwrap())),
            end: None,
        };
        let records = build_records(&[entry(Some(range), Vec::new())]);

        assert_eq!(RangeUnit::Invalid, records[0].range);
    }

    #[test]
    fn invalid_address_byte_length_is_invalid() {
        let range = IpRange {
            start: Some(IpAddress { addr: vec![0u8; 5] }),
            end: Some(IpAddress::from("10.0.0.1".parse::<IpAddr>().unwrap())),
        };
        let records = build_records(&[entry(Some(range), Vec::new())]);

        assert_eq!(RangeUnit::Invalid, records[0].range);
    }

    #[test]
    fn address_family_mismatch_is_invalid() {
        let range = IpRange {
            start: Some(IpAddress::from("10.0.0.1".parse::<IpAddr>().unwrap())),
            end: Some(IpAddress::from("2001:db8::1".parse::<IpAddr>().unwrap())),
        };
        let records = build_records(&[entry(Some(range), Vec::new())]);

        assert_eq!(RangeUnit::Invalid, records[0].range);
    }

    #[test]
    fn inverted_range_is_invalid() {
        let records = build_records(&[entry(Some(ip_range("10.0.0.254", "10.0.0.1")), Vec::new())]);

        assert_eq!(RangeUnit::Invalid, records[0].range);
    }

    #[test]
    fn well_formed_range_is_not_invalid() {
        let records = build_records(&[entry(Some(ip_range("10.0.0.1", "10.0.0.1")), Vec::new())]);

        assert_ne!(RangeUnit::Invalid, records[0].range);
    }

    #[test]
    fn invalid_range_preserves_nexthops() {
        let records = build_records(&[entry(None, vec![nexthop("vlan100")])]);

        assert_eq!(RangeUnit::Invalid, records[0].range);
        assert_eq!(1, records[0].nexthops.len());
    }
}
