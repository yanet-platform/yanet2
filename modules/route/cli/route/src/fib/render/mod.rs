//! Human-readable table rendering for `fib show`: one row per entry, or one
//! row per nexthop for an ECMP group.
//!
//! Every entry contributes at least one row, so an entry with no nexthops
//! still shows up rather than vanishing. No `Width`/`Wrap` setting is ever
//! applied, matching how borderless CLI table tools conventionally behave:
//! a narrow terminal is left to wrap the output itself rather than the
//! table pre-wrapping it.

use tabled::{
    settings::{object::Rows, Color, Padding, Style},
    Table, Tabled,
};
use ync::output;

use super::range_endpoints;
use crate::routepb::{FibEntry, FibNexthop};

/// One table row: either a full entry (or its first nexthop), or a
/// continuation row for a later ECMP nexthop with `from`/`to` left empty.
#[derive(Debug, Tabled)]
struct FibRow {
    #[tabled(rename = "From")]
    from: String,
    #[tabled(rename = "To")]
    to: String,
    #[tabled(rename = "Device")]
    device: String,
    #[tabled(rename = "Dst MAC")]
    dst_mac: String,
    #[tabled(rename = "Src MAC")]
    src_mac: String,
    #[tabled(rename = "Counter")]
    counter: String,
}

/// Strips every `char::is_control` character out of `value`, replacing each
/// with `?`.
///
/// Applied to a device name or counter name before either is pushed into a
/// cell: both are echoed verbatim from the wire, and neither is validated
/// for control characters upstream, so a peer-embedded control character --
/// an ANSI escape, say -- would otherwise reach the terminal unfiltered.
fn sanitize_wire_text(value: &str) -> String {
    value.chars().map(|ch| if ch.is_control() { '?' } else { ch }).collect()
}

/// Builds the single-row placeholder for an entry with no nexthops.
fn no_nexthops_row(from: String, to: String) -> FibRow {
    FibRow {
        from,
        to,
        device: "(no nexthops)".to_owned(),
        dst_mac: String::new(),
        src_mac: String::new(),
        counter: String::new(),
    }
}

/// Builds one row per nexthop in an ECMP group.
///
/// Only the first row carries the entry's `from`/`to` cells; the rest are
/// continuation rows with those cells left empty, which is what tells them
/// apart from a new entry's own row when the table is read back.
fn nexthop_rows(from: String, to: String, nexthops: &[FibNexthop]) -> Vec<FibRow> {
    nexthops
        .iter()
        .enumerate()
        .map(|(index, nexthop)| FibRow {
            from: if index == 0 { from.clone() } else { String::new() },
            to: if index == 0 { to.clone() } else { String::new() },
            device: sanitize_wire_text(&nexthop.device),
            dst_mac: nexthop.dst_mac.unwrap_or_default().to_string(),
            src_mac: nexthop.src_mac.unwrap_or_default().to_string(),
            counter: sanitize_wire_text(&nexthop.counter),
        })
        .collect()
}

/// Builds the row(s) for one [`FibEntry`].
fn build_rows(entry: &FibEntry) -> Vec<FibRow> {
    let (from, to) = range_endpoints(entry.range.as_ref());

    if entry.nexthops.is_empty() {
        return vec![no_nexthops_row(from, to)];
    }

    nexthop_rows(from, to, &entry.nexthops)
}

/// Renders `entries` as a headered, borderless table.
///
/// `colored` gates only the header row's bold styling: every cell's content
/// renders identically regardless of colour mode.
fn render_table(entries: &[FibEntry], colored: bool) -> String {
    let rows: Vec<FibRow> = entries.iter().flat_map(build_rows).collect();

    let mut table = Table::new(&rows);
    table.with(Style::empty()).with(Padding::new(0, 2, 0, 0));

    if colored {
        table.modify(Rows::first(), Color::BOLD);
    }

    table.to_string()
}

/// Prints `entries` as the `fib show` human view.
pub fn print_fib(entries: &[FibEntry]) {
    let colored = output::is_colored();

    for line in render_table(entries, colored).lines() {
        println!("{}", line.trim_end());
    }
}

#[cfg(test)]
mod test {
    use core::net::IpAddr;

    use commonpb::pb::IpRange;

    use super::*;

    #[test]
    fn entry_with_no_nexthops_renders_one_placeholder_row() {
        let entry = FibEntry {
            range: Some(IpRange::from((
                "10.0.0.0".parse::<IpAddr>().unwrap(),
                "10.0.0.255".parse::<IpAddr>().unwrap(),
            ))),
            nexthops: Vec::new(),
        };

        let rows = build_rows(&entry);

        assert_eq!(1, rows.len());
        assert_eq!("10.0.0.0", rows[0].from);
        assert_eq!("10.0.0.255", rows[0].to);
        assert_eq!("(no nexthops)", rows[0].device);
    }
}
