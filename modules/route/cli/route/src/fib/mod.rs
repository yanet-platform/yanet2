//! Support code for `fib show`.
//!
//! [`render`] turns the wire's `FibEntry` rows -- see
//! [`crate::routepb::FibEntry`] -- into the human table, one row per entry
//! or one row per nexthop for an ECMP group, in wire order. `--format
//! json` serializes `FibEntry` directly instead: `build.rs` derives
//! `Serialize` on it, so there is no view of its own here for that format.
//! The server validates every range before ShowFIB's response can carry
//! it, and the data itself comes from an LPM dump that cannot produce a
//! malformed or inverted pair, so [`render`] does not reclassify or decode
//! a range either. This module only holds the formatting helper
//! [`render`] uses: [`range_endpoints`] for a range's two endpoints.

pub mod render;

use commonpb::pb::{IpAddress, IpRange};

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
