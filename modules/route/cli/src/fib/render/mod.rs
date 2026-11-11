//! Human-readable table rendering for `fib show`.
//!
//! A block carries its key on the first row only and lists one row per
//! nexthop, or one placeholder row when there is none. The range view
//! prints a block per entry, the CIDR view a block per prefix covering the
//! entry, each with all of the entry's nexthops. The table is never
//! pre-wrapped: as with other borderless CLI tables, a long line is left to
//! the terminal or the pager.

use tabled::{
    Table, Tabled,
    settings::{Color, Padding, Style, object::Rows},
};
use ync::output;

use super::{range_endpoints, range_prefixes};
use crate::routepb::{FibEntry, FibNexthop};

/// The nexthop columns every row ends with.
#[derive(Debug, Clone, Tabled)]
struct NexthopCells {
    #[tabled(rename = "Device")]
    device: String,
    #[tabled(rename = "Dst MAC")]
    dst_mac: String,
    #[tabled(rename = "Src MAC")]
    src_mac: String,
    #[tabled(rename = "Counter")]
    counter: String,
}

/// A row of the range view; a continuation row leaves both endpoints empty.
#[derive(Debug, Tabled)]
struct RangeRow {
    #[tabled(rename = "From")]
    from: String,
    #[tabled(rename = "To")]
    to: String,
    #[tabled(inline)]
    nexthop: NexthopCells,
}

/// A row of the CIDR view; a continuation row leaves the prefix empty.
#[derive(Debug, Tabled)]
struct PrefixRow {
    #[tabled(rename = "Prefix")]
    prefix: String,
    #[tabled(inline)]
    nexthop: NexthopCells,
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

/// Builds one row's nexthop cells per nexthop, or a single `(no nexthops)`
/// placeholder so an entry without nexthops still shows up.
fn nexthop_cells(nexthops: &[FibNexthop]) -> Vec<NexthopCells> {
    if nexthops.is_empty() {
        return vec![NexthopCells {
            device: "(no nexthops)".to_owned(),
            dst_mac: String::new(),
            src_mac: String::new(),
            counter: String::new(),
        }];
    }

    nexthops
        .iter()
        .map(|nexthop| NexthopCells {
            device: sanitize_wire_text(&nexthop.device),
            dst_mac: nexthop.dst_mac.unwrap_or_default().to_string(),
            src_mac: nexthop.src_mac.unwrap_or_default().to_string(),
            counter: sanitize_wire_text(&nexthop.counter),
        })
        .collect()
}

/// Builds one block's rows, with the key on the first row only.
///
/// The empty key of a continuation row is what tells it apart from a new
/// block's own row when the table is read back.
fn block<K, R>(key: K, nexthops: Vec<NexthopCells>, row: impl Fn(K, NexthopCells) -> R) -> Vec<R>
where
    K: Default,
{
    let mut key = Some(key);
    nexthops
        .into_iter()
        .map(|nexthop| row(key.take().unwrap_or_default(), nexthop))
        .collect()
}

/// Builds the range view rows of an entry: one block keyed by its endpoints.
fn range_rows(entry: &FibEntry) -> Vec<RangeRow> {
    let nexthops = nexthop_cells(&entry.nexthops);

    block(
        range_endpoints(entry.range.as_ref()),
        nexthops,
        |(from, to), nexthop| RangeRow { from, to, nexthop },
    )
}

/// Builds the CIDR view rows of an entry: a block per covering prefix.
fn prefix_rows(entry: &FibEntry) -> Vec<PrefixRow> {
    let nexthops = nexthop_cells(&entry.nexthops);

    range_prefixes(entry.range.as_ref())
        .into_iter()
        .flat_map(|prefix| {
            block(prefix, nexthops.clone(), |prefix, nexthop| PrefixRow {
                prefix,
                nexthop,
            })
        })
        .collect()
}

/// Renders the rows as a headered, borderless table.
///
/// Colour only bolds the header row: every cell's content renders
/// identically regardless of colour mode.
fn render_table<R>(rows: impl IntoIterator<Item = R>, colored: bool) -> String
where
    R: Tabled,
{
    let mut table = Table::new(rows);
    table.with(Style::empty()).with(Padding::new(0, 2, 0, 0));

    if colored {
        table.modify(Rows::first(), Color::BOLD);
    }

    table.to_string()
}

/// Prints the `fib show` human view: by prefix under `--cidr`, by range
/// otherwise.
pub fn print_fib(entries: &[FibEntry], cidr: bool) {
    let colored = output::is_colored();
    let table = if cidr {
        render_table(entries.iter().flat_map(prefix_rows), colored)
    } else {
        render_table(entries.iter().flat_map(range_rows), colored)
    };

    for line in table.lines() {
        println!("{}", line.trim_end());
    }
}

#[cfg(test)]
mod test {
    use core::net::IpAddr;

    use commonpb::pb::IpRange;

    use super::*;

    /// Builds a present range between two well-formed addresses.
    fn ip_range(start: &str, end: &str) -> Option<IpRange> {
        Some(IpRange::from((
            start.parse::<IpAddr>().unwrap(),
            end.parse::<IpAddr>().unwrap(),
        )))
    }

    /// Builds a nexthop on a device, without MACs or a counter.
    fn nexthop(device: &str) -> FibNexthop {
        FibNexthop {
            dst_mac: None,
            src_mac: None,
            device: device.to_owned(),
            counter: String::new(),
        }
    }

    #[test]
    fn test_range_rows_entry_without_nexthops_keeps_placeholder_row() {
        let entry = FibEntry {
            range: ip_range("10.0.0.0", "10.0.0.255"),
            nexthops: Vec::new(),
        };

        let rows = range_rows(&entry);

        assert_eq!(1, rows.len());
        assert_eq!("10.0.0.0", rows[0].from);
        assert_eq!("10.0.0.255", rows[0].to);
        assert_eq!("(no nexthops)", rows[0].nexthop.device);
    }

    #[test]
    fn test_prefix_rows_repeat_nexthops_under_each_prefix() {
        let entry = FibEntry {
            range: ip_range("10.0.1.0", "10.0.3.255"),
            nexthops: vec![nexthop("eth1"), nexthop("eth2")],
        };

        let rows = prefix_rows(&entry);
        let cells: Vec<(&str, &str)> = rows
            .iter()
            .map(|row| (row.prefix.as_str(), row.nexthop.device.as_str()))
            .collect();

        assert_eq!(
            vec![
                ("10.0.1.0/24", "eth1"),
                ("", "eth2"),
                ("10.0.2.0/23", "eth1"),
                ("", "eth2")
            ],
            cells
        );
    }
}
