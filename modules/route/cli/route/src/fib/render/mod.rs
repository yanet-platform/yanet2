//! Human-readable table rendering for `fib show`: one row per record, or one
//! row per nexthop for an ECMP group.
//!
//! Every record contributes at least one row, so a record with an invalid
//! range or with no nexthops still shows up rather than vanishing. No
//! `Width`/`Wrap` setting is ever applied, matching how borderless CLI table
//! tools conventionally behave: a narrow terminal is left to wrap the output
//! itself rather than the table pre-wrapping it.

use core::net::IpAddr;

use tabled::{
    settings::{object::Rows, Color, Padding, Style},
    Table, Tabled,
};
use ync::output;

use super::{format_mac, FibRecord, RangeUnit};
use crate::routepb::FibNexthop;

/// One table row: either a full record (or its first nexthop), or a
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
}

/// Strips every `char::is_control` character out of `value`, replacing each
/// with `?`.
///
/// Applied to a device name before it is pushed into a cell: `device` is
/// echoed verbatim from the wire with no upstream validation beyond
/// non-emptiness, so a control character a peer embedded in it -- an ANSI
/// escape, say -- would otherwise reach the terminal unfiltered.
fn sanitize_wire_text(value: &str) -> String {
    value.chars().map(|ch| if ch.is_control() { '?' } else { ch }).collect()
}

/// Builds the single-row placeholder for an invalid range.
fn invalid_row() -> FibRow {
    FibRow {
        from: "(invalid range)".to_owned(),
        to: String::new(),
        device: String::new(),
        dst_mac: String::new(),
        src_mac: String::new(),
    }
}

/// Builds the single-row placeholder for a well-formed range with no
/// nexthops.
fn no_nexthops_row(start: IpAddr, end: IpAddr) -> FibRow {
    FibRow {
        from: start.to_string(),
        to: end.to_string(),
        device: "(no nexthops)".to_owned(),
        dst_mac: String::new(),
        src_mac: String::new(),
    }
}

/// Builds one row per nexthop in an ECMP group.
///
/// Only the first row carries the range's `from`/`to` cells; the rest are
/// continuation rows with those cells left empty, which is what tells them
/// apart from a new record's own row when the table is read back.
fn nexthop_rows(start: IpAddr, end: IpAddr, nexthops: &[FibNexthop]) -> Vec<FibRow> {
    let from_text = start.to_string();
    let to_text = end.to_string();

    nexthops
        .iter()
        .enumerate()
        .map(|(index, nexthop)| FibRow {
            from: if index == 0 { from_text.clone() } else { String::new() },
            to: if index == 0 { to_text.clone() } else { String::new() },
            device: sanitize_wire_text(&nexthop.device),
            dst_mac: format_mac(nexthop.dst_mac),
            src_mac: format_mac(nexthop.src_mac),
        })
        .collect()
}

/// Builds the row(s) for one [`FibRecord`].
///
/// [`RangeUnit::Invalid`] is matched first and unconditionally, ahead of
/// the nexthop-count check on the `Range` arms: an invalid record may now
/// carry nexthops cloned from the wire, but the human view still
/// summarises it down to the single `(invalid range)` placeholder row,
/// never one row per nexthop.
fn build_rows(record: &FibRecord) -> Vec<FibRow> {
    match record.range {
        RangeUnit::Invalid => vec![invalid_row()],
        RangeUnit::Range { start, end } if record.nexthops.is_empty() => {
            vec![no_nexthops_row(start, end)]
        }
        RangeUnit::Range { start, end } => nexthop_rows(start, end, &record.nexthops),
    }
}

/// Renders `records` as a headered, borderless table.
///
/// `colored` gates only the header row's bold styling: every cell's content
/// renders identically regardless of colour mode.
fn render_table(records: &[FibRecord], colored: bool) -> String {
    let rows: Vec<FibRow> = records.iter().flat_map(build_rows).collect();

    let mut table = Table::new(&rows);
    table.with(Style::empty()).with(Padding::new(0, 2, 0, 0));

    if colored {
        table.modify(Rows::first(), Color::BOLD);
    }

    table.to_string()
}

/// Prints `records` as the `fib show` human view.
pub fn print_fib(records: &[FibRecord]) {
    let colored = output::is_colored();

    for line in render_table(records, colored).lines() {
        println!("{}", line.trim_end());
    }
}
