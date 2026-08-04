//! Support code for `fib show`.
//!
//! [`render`] and [`json`] each turn the wire's `FibEntry` rows -- see
//! [`crate::routepb::FibEntry`] -- straight into their output, in wire
//! order: [`json`] emits one record per entry, [`render`] emits one row
//! per entry or one row per nexthop for an ECMP group. The server
//! validates every range before ShowFIB's response can carry it, and the
//! data itself comes from an LPM dump that cannot produce a malformed or
//! inverted pair, so neither view reclassifies or decodes a range here.
//! This module only holds the two formatting helpers they share:
//! [`format_mac`] for a nexthop's MAC address, and [`range_endpoints`]
//! for a range's two endpoints.

pub mod json;
pub mod render;

use commonpb::pb::{IpAddress, IpRange, MacAddress};
use netip::MacAddr;

/// The literal rendered for a range endpoint that is absent.
///
/// Matches the literal [`commonpb::pb::IpAddress`]'s own `Display` falls
/// back to for a malformed byte length, so an absent endpoint and a
/// malformed one render identically instead of the absent one silently
/// becoming an empty cell.
const INVALID_ENDPOINT: &str = "invalid";

/// Renders a range's two endpoints.
///
/// An absent `range`, or an absent endpoint within a present one, renders
/// as [`INVALID_ENDPOINT`] -- the same literal a present-but-malformed
/// endpoint's `Display` already falls back to.
pub(crate) fn range_endpoints(range: Option<&IpRange>) -> (String, String) {
    let start = range.and_then(|range| range.start.as_ref());
    let end = range.and_then(|range| range.end.as_ref());
    (format_endpoint(start), format_endpoint(end))
}

/// Renders one range endpoint, or [`INVALID_ENDPOINT`] if it is absent.
fn format_endpoint(addr: Option<&IpAddress>) -> String {
    addr.map(IpAddress::to_string)
        .unwrap_or_else(|| INVALID_ENDPOINT.to_owned())
}

/// Renders a nexthop's MAC address, or a fallback for a missing/invalid one.
pub(crate) fn format_mac(mac: Option<MacAddress>) -> String {
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
    use core::net::IpAddr;

    use super::*;

    #[test]
    fn range_endpoints_renders_well_formed_addresses() {
        let range = IpRange {
            start: Some(IpAddress::from("10.0.0.0".parse::<IpAddr>().unwrap())),
            end: Some(IpAddress::from("10.0.0.255".parse::<IpAddr>().unwrap())),
        };

        assert_eq!(
            ("10.0.0.0".to_owned(), "10.0.0.255".to_owned()),
            range_endpoints(Some(&range))
        );
    }

    #[test]
    fn range_endpoints_treats_absent_range_as_invalid() {
        assert_eq!(("invalid".to_owned(), "invalid".to_owned()), range_endpoints(None));
    }

    #[test]
    fn range_endpoints_treats_missing_endpoint_as_invalid() {
        let range = IpRange {
            start: Some(IpAddress::from("10.0.0.1".parse::<IpAddr>().unwrap())),
            end: None,
        };

        assert_eq!(
            ("10.0.0.1".to_owned(), "invalid".to_owned()),
            range_endpoints(Some(&range))
        );
    }
}
