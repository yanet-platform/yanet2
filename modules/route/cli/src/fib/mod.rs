//! Support code for `fib show`.
//!
//! The human view renders the wire entries in wire order, by range or,
//! under `--cidr`, by the prefixes covering each range. JSON serializes the
//! wire entries directly and never decomposes a range. The server validates
//! every range before the response carries it, so the range view prints
//! the endpoints as they arrive. A range the CIDR view cannot decompose
//! still prints, as a single `invalid` prefix.

pub mod render;

use commonpb::pb::{IpAddress, IpRange};

/// The literal rendered for an address or prefix a range cannot supply.
///
/// Matches the literal [`commonpb::pb::IpAddress`]'s own `Display` falls
/// back to for a malformed byte length, so an absent endpoint and a
/// malformed one render identically instead of the absent one silently
/// becoming an empty cell.
const INVALID: &str = "invalid";

/// Renders a range's two endpoints.
///
/// An absent `range`, or an absent endpoint within a present one, renders
/// as [`INVALID`] -- the same literal a present-but-malformed endpoint's
/// `Display` already falls back to.
pub(crate) fn range_endpoints(range: Option<&IpRange>) -> (String, String) {
    let start = range.and_then(|range| range.start.as_ref());
    let end = range.and_then(|range| range.end.as_ref());
    (format_endpoint(start), format_endpoint(end))
}

/// Renders one range endpoint, or [`INVALID`] if it is absent.
fn format_endpoint(addr: Option<&IpAddress>) -> String {
    addr.map(IpAddress::to_string).unwrap_or_else(|| INVALID.to_owned())
}

/// Renders the CIDR prefixes that cover a range, in ascending order.
///
/// A range that yields no prefix, such as an absent, inverted or malformed
/// one, renders as the single prefix `invalid`, so its entry keeps its rows
/// instead of vanishing from the listing.
pub fn range_prefixes(range: Option<&IpRange>) -> Vec<String> {
    let prefixes: Vec<String> = range
        .into_iter()
        .flat_map(IpRange::cidrs)
        .map(|prefix| prefix.to_string())
        .collect();

    if prefixes.is_empty() {
        return vec![INVALID.to_owned()];
    }

    prefixes
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

    #[test]
    fn test_range_prefixes_absent_range_is_invalid() {
        assert_eq!(vec!["invalid".to_owned()], range_prefixes(None));
    }
}
