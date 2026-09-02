//! Histogram section of the report: series grouped by metric name.
//!
//! Every metric name becomes one block. Labels shared by all of its series
//! move into the block header and the rest become table columns, so a
//! block of gRPC handling latencies reads as a table of methods rather
//! than as eleven copies of the same three-label brace.

use core::cmp::Ordering;
use std::collections::BTreeMap;

use commonpb::pb::{Histogram, Label, Metric, metric::Value};
use tabled::{
    builder::Builder,
    settings::{Alignment, Margin, object::Columns},
};
use ync::{
    display::{self, Glyphs},
    histogram::{BucketRow, Unit, bucket_label, bucket_rows, format_share, quantile},
    metrics::format_number,
    output,
};

use crate::format_labels;

/// Percentiles shown for every series, as percents.
const PERCENTILES: [f64; 3] = [50.0, 90.0, 99.0];

/// Widest bar of a bucket listing, in cells.
const BAR_WIDTH: usize = 32;

/// Narrowest bar of a bucket listing on a cramped terminal, in cells.
const BAR_WIDTH_MIN: usize = 8;

/// Indent of a series' lines under its block header.
const SERIES_INDENT: usize = 2;

/// One histogram series: the wire metric and its histogram value.
pub struct Series<'a> {
    metric: &'a Metric,
    histogram: &'a Histogram,
}

/// Every series of one metric name, with its labels split into the ones
/// that vary across the series and the ones they all share.
pub struct Group<'a> {
    pub name: &'a str,
    pub unit: Unit,
    pub constant: Vec<&'a Label>,
    pub varying: Vec<&'a str>,
    pub series: Vec<Series<'a>>,
}

/// Groups the histogram metrics of a response by name.
///
/// Groups come out sorted by name and the series inside a group by their
/// varying label values, comparing digit runs as numbers so worker 2
/// precedes worker 10. Label columns keep the order the first series
/// listed them in.
pub fn group(metrics: &[Metric]) -> Vec<Group<'_>> {
    let mut by_name: BTreeMap<&str, Vec<Series<'_>>> = BTreeMap::new();

    for metric in metrics {
        if let Some(Value::Histogram(histogram)) = &metric.value {
            by_name
                .entry(metric.name.as_str())
                .or_default()
                .push(Series { metric, histogram });
        }
    }

    by_name
        .into_iter()
        .map(|(name, mut series)| {
            let mut keys: Vec<&str> = Vec::new();

            for label in series.iter().flat_map(|series| &series.metric.labels) {
                if !keys.contains(&label.name.as_str()) {
                    keys.push(&label.name);
                }
            }

            let (varying, constant): (Vec<&str>, Vec<&str>) = keys.iter().partition(|key| {
                let first = label_value(series[0].metric, key);

                series.iter().any(|series| label_value(series.metric, key) != first)
            });

            let constant = constant
                .iter()
                .filter_map(|key| series[0].metric.labels.iter().find(|label| label.name == *key))
                .collect();

            series.sort_by(|a, b| {
                varying
                    .iter()
                    .map(|key| {
                        natural_cmp(
                            label_value(a.metric, key).unwrap_or(""),
                            label_value(b.metric, key).unwrap_or(""),
                        )
                    })
                    .find(|ordering| ordering.is_ne())
                    .unwrap_or(Ordering::Equal)
            });

            Group {
                name,
                unit: Unit::of(name),
                constant,
                varying,
                series,
            }
        })
        .collect()
}

/// Prints the one-line-per-series table of a group.
pub fn print_summary(group: &Group<'_>, glyphs: &Glyphs) {
    print_header(group, glyphs);

    let mut builder = Builder::new();

    for row in summary_rows(group, glyphs, display::terminal_width()) {
        builder.push_record(row);
    }

    let numeric = group.varying.len()..group.varying.len() + 1 + PERCENTILES.len();
    let mut table = builder.build();
    table.modify(Columns::new(numeric), Alignment::right());
    display::print_table(table);
}

/// Builds the summary table of a group, header row first.
///
/// Label columns first, then the observation count, the percentiles and,
/// when the glyph set has one, a sparkline over every bucket so the rows
/// share one axis and compare at a glance. A sparkline that would not fit
/// in `columns` is dropped rather than wrapped: broken across lines it
/// shows nothing, and the bucket listing still carries the shape.
fn summary_rows(group: &Group<'_>, glyphs: &Glyphs, columns: Option<usize>) -> Vec<Vec<String>> {
    let mut headers: Vec<String> = group.varying.iter().map(|key| (*key).to_owned()).collect();
    headers.push("count".to_owned());
    headers.extend(PERCENTILES.iter().map(|percent| format!("p{percent:.0}")));

    if glyphs.has_sparkline() {
        headers.push("distribution".to_owned());
    }

    let mut rows: Vec<Vec<String>> = vec![headers];

    for series in &group.series {
        let buckets = &series.histogram.buckets;
        let mut row: Vec<String> = group
            .varying
            .iter()
            .map(|key| label_value(series.metric, key).unwrap_or("").to_owned())
            .collect();

        row.push(format_number(series.histogram.total_count));
        row.extend(
            PERCENTILES
                .iter()
                .map(|&percent| quantile(buckets, percent).render(group.unit)),
        );

        let counts: Vec<u64> = buckets.iter().map(|bucket| bucket.count).collect();

        if let Some(line) = display::sparkline(&counts, glyphs) {
            row.push(line);
        }

        rows.push(row);
    }

    if glyphs.has_sparkline() && columns.is_some_and(|columns| table_width(&rows) > columns) {
        for row in &mut rows {
            row.pop();
        }
    }

    rows
}

/// Prints every series of a group as a bucket listing.
///
/// Each series gets its distinguishing labels, a stats line and a table
/// of the buckets worth showing, with the share of observations next to
/// a bar so a bucket dwarfed by a dominant neighbour still has a number.
pub fn print_buckets(group: &Group<'_>, glyphs: &Glyphs) {
    print_header(group, glyphs);

    let indent = " ".repeat(SERIES_INDENT);

    for (position, series) in group.series.iter().enumerate() {
        if position > 0 {
            println!();
        }

        let buckets = &series.histogram.buckets;

        if !group.varying.is_empty() {
            let own: Vec<&Label> = series
                .metric
                .labels
                .iter()
                .filter(|label| group.varying.contains(&label.name.as_str()))
                .collect();
            let identity = if own.is_empty() {
                "-".to_owned()
            } else {
                format_labels(own.into_iter())
            };

            println!("{indent}{identity}");
        }

        let stats: Vec<String> = PERCENTILES
            .iter()
            .map(|&percent| format!("p{percent:.0} {}", quantile(buckets, percent).render(group.unit)))
            .collect();

        println!(
            "{indent}count {}   {}",
            format_number(series.histogram.total_count),
            stats.join("   ")
        );

        let rows = bucket_rows(buckets);

        if rows.is_empty() {
            println!("{indent}{indent}{}", output::dim("no observations"));
            continue;
        }

        let total: u64 = buckets.iter().map(|bucket| bucket.count).sum();
        let max = buckets.iter().map(|bucket| bucket.count).max().unwrap_or(0);
        let label = |index| bucket_label(buckets, index, group.unit);

        // The bar column starts empty so the text columns alone decide how
        // much of the terminal is left for it.
        let mut cells: Vec<Vec<String>> = vec![vec![
            glyphs.at_most.to_owned(),
            "count".to_owned(),
            "share".to_owned(),
            String::new(),
        ]];

        cells.extend(rows.iter().map(|row| match *row {
            BucketRow::Bucket(index) => vec![
                label(index),
                format_number(buckets[index].count),
                format_share(buckets[index].count, total),
                String::new(),
            ],
            BucketRow::EmptyRun { first, last } => vec![
                format!("{} {} {}", label(first), glyphs.ellipsis, label(last)),
                "0".to_owned(),
                format_share(0, total),
                String::new(),
            ],
        }));

        let width = bar_width(&cells);

        for (row, cell) in rows.iter().zip(cells.iter_mut().skip(1)) {
            if let BucketRow::Bucket(index) = *row {
                cell[3] = display::bar(buckets[index].count, max, width, glyphs);
            }
        }

        let mut builder = Builder::new();

        for cell in cells {
            builder.push_record(cell);
        }

        let mut table = builder.build();
        table.modify(Columns::new(..3), Alignment::right());
        table.with(Margin::new(SERIES_INDENT * 2, 0, 0, 0));
        display::print_table(table);
    }
}

/// Prints the block header: the name, the labels every series shares and,
/// dimmed, the bucket count with the finite bound range so a sparkline
/// can be read against its axis.
///
/// The range describes the first series. Series of one name share their
/// bucket layout in every producer today, so the header speaks for all of
/// them.
fn print_header(group: &Group<'_>, glyphs: &Glyphs) {
    let mut line = group.name.to_owned();

    if !group.constant.is_empty() {
        line.push_str("  ");
        line.push_str(&format_labels(group.constant.iter().copied()));
    }

    let buckets = &group.series[0].histogram.buckets;
    let mut finite = buckets
        .iter()
        .map(|bucket| bucket.upper_bound)
        .filter(|bound| bound.is_finite());
    let noun = if buckets.len() == 1 { "bucket" } else { "buckets" };
    let mut meta = format!("{} {noun}", buckets.len());

    if let Some(first) = finite.next() {
        let last = finite.next_back().unwrap_or(first);
        meta = format!(
            "{meta} {} {} {}",
            group.unit.format(first),
            glyphs.ellipsis,
            group.unit.format(last)
        );
    }

    println!("{line}   {}", output::dim(&meta));
}

/// Picks the bar width that keeps a bucket listing inside the terminal.
///
/// `cells` are the listing's rows with an empty bar column, so their table
/// width is exactly the part the bar cannot have. Off a terminal the bar
/// takes its full width.
fn bar_width(cells: &[Vec<String>]) -> usize {
    let Some(columns) = display::terminal_width() else {
        return BAR_WIDTH;
    };

    columns
        .saturating_sub(SERIES_INDENT * 2 + table_width(cells))
        .clamp(BAR_WIDTH_MIN, BAR_WIDTH)
}

/// Width in columns the shared table style needs for `rows`, header
/// included: every cell padded by one space on each side and one border
/// between neighbouring columns.
fn table_width(rows: &[Vec<String>]) -> usize {
    let columns = rows.first().map_or(0, Vec::len);
    let text: usize = (0..columns)
        .map(|column| {
            rows.iter()
                .map(|row| row.get(column).map_or(0, |cell| cell.chars().count()))
                .max()
                .unwrap_or(0)
        })
        .sum();

    text + 3 * columns - 1
}

/// Returns the value of the label named `key` on `metric`, if present.
fn label_value<'a>(metric: &'a Metric, key: &str) -> Option<&'a str> {
    metric
        .labels
        .iter()
        .find(|label| label.name == key)
        .map(|label| label.value.as_str())
}

/// Orders two label values so that digit runs compare as numbers.
///
/// A plain string order puts `10` before `2`, which scrambles worker,
/// queue and port indices that are the most common label values.
fn natural_cmp(a: &str, b: &str) -> Ordering {
    let mut a = chunks(a).into_iter();
    let mut b = chunks(b).into_iter();

    loop {
        let ordering = match (a.next(), b.next()) {
            (None, None) => return Ordering::Equal,
            (None, Some(_)) => return Ordering::Less,
            (Some(_), None) => return Ordering::Greater,
            (Some(x), Some(y)) => match (x.parse::<u64>(), y.parse::<u64>()) {
                (Ok(x), Ok(y)) => x.cmp(&y),
                _ => x.cmp(y),
            },
        };

        if ordering.is_ne() {
            return ordering;
        }
    }
}

/// Splits `text` into alternating runs of digits and non-digits.
fn chunks(text: &str) -> Vec<&str> {
    let mut chunks = Vec::new();
    let mut start = 0;
    let mut digits = None;

    for (offset, ch) in text.char_indices() {
        let is_digit = ch.is_ascii_digit();

        if digits.is_some_and(|current| current != is_digit) {
            chunks.push(&text[start..offset]);
            start = offset;
        }

        digits = Some(is_digit);
    }

    if start < text.len() {
        chunks.push(&text[start..]);
    }

    chunks
}

#[cfg(test)]
mod test {
    use commonpb::pb::Bucket;

    use super::*;

    /// Builds a histogram metric with the given labels and no buckets.
    fn metric(name: &str, labels: &[(&str, &str)]) -> Metric {
        Metric {
            name: name.to_owned(),
            labels: labels
                .iter()
                .map(|&(name, value)| Label {
                    name: name.to_owned(),
                    value: value.to_owned(),
                })
                .collect(),
            value: Some(Value::Histogram(Histogram { buckets: Vec::new(), total_count: 0 })),
        }
    }

    /// Builds a `worker_rx_bursts`-shaped metric: 34 buckets with every
    /// observation in the first one, so its sparkline is 34 columns wide.
    fn bursts(worker: &str) -> Metric {
        let buckets = (0..=32)
            .map(|bound| Bucket {
                count: if bound == 0 { 1_000 } else { 0 },
                upper_bound: f64::from(bound),
            })
            .chain(core::iter::once(Bucket { count: 0, upper_bound: f64::INFINITY }))
            .collect();

        Metric {
            name: "worker_rx_bursts".to_owned(),
            labels: vec![Label {
                name: "worker_idx".to_owned(),
                value: worker.to_owned(),
            }],
            value: Some(Value::Histogram(Histogram { buckets, total_count: 1_000 })),
        }
    }

    /// Returns the values of the label named `key` across a group's series,
    /// in series order.
    fn column<'a>(group: &'a Group<'_>, key: &str) -> Vec<&'a str> {
        group
            .series
            .iter()
            .map(|series| label_value(series.metric, key).unwrap_or(""))
            .collect()
    }

    #[test]
    fn test_group_lifts_shared_labels_into_the_header() {
        let metrics = [
            metric("h", &[("method", "Get"), ("service", "svc"), ("kind", "unary")]),
            metric("h", &[("method", "Put"), ("service", "svc"), ("kind", "unary")]),
        ];

        let groups = group(&metrics);

        assert_eq!(1, groups.len());
        assert_eq!(vec!["method"], groups[0].varying);
        assert_eq!(
            vec!["service", "kind"],
            groups[0]
                .constant
                .iter()
                .map(|label| label.name.as_str())
                .collect::<Vec<_>>()
        );
    }

    #[test]
    fn test_group_single_series_has_only_constant_labels() {
        let metrics = [metric("h", &[("gateway", "numa0")])];

        let groups = group(&metrics);

        assert!(groups[0].varying.is_empty());
        assert_eq!("numa0", groups[0].constant[0].value);
    }

    #[test]
    fn test_group_label_missing_on_one_series_is_varying() {
        let metrics = [metric("h", &[("config", "acl0")]), metric("h", &[])];

        let groups = group(&metrics);

        assert_eq!(vec!["config"], groups[0].varying);
        assert!(groups[0].constant.is_empty());
    }

    #[test]
    fn test_group_sorts_series_numerically_by_label_value() {
        let metrics = [
            metric("h", &[("worker_idx", "10")]),
            metric("h", &[("worker_idx", "2")]),
            metric("h", &[("worker_idx", "1")]),
        ];

        let groups = group(&metrics);

        assert_eq!(vec!["1", "2", "10"], column(&groups[0], "worker_idx"));
    }

    #[test]
    fn test_group_orders_groups_by_metric_name_and_skips_scalars() {
        let mut metrics = vec![metric("b_seconds", &[]), metric("a_bytes", &[])];
        metrics.push(Metric {
            name: "a_total".to_owned(),
            labels: Vec::new(),
            value: Some(Value::Counter(1)),
        });

        let groups = group(&metrics);

        assert_eq!(
            vec![("a_bytes", Unit::Bytes), ("b_seconds", Unit::Seconds)],
            groups.iter().map(|group| (group.name, group.unit)).collect::<Vec<_>>()
        );
    }

    #[test]
    fn test_summary_rows_keep_the_sparkline_when_it_fits() {
        let metrics = [bursts("0")];
        let groups = group(&metrics);

        let rows = summary_rows(&groups[0], &Glyphs::unicode(), Some(120));

        assert_eq!("distribution", rows[0].last().map(String::as_str).unwrap());
        assert_eq!(34, rows[1].last().map(|cell| cell.chars().count()).unwrap());
    }

    #[test]
    fn test_summary_rows_drop_the_sparkline_on_a_narrow_terminal() {
        let metrics = [bursts("0")];
        let groups = group(&metrics);

        let rows = summary_rows(&groups[0], &Glyphs::unicode(), Some(60));

        assert_eq!("p99", rows[0].last().map(String::as_str).unwrap());
        assert!(rows.iter().all(|row| row.len() == rows[0].len()));
    }

    #[test]
    fn test_summary_rows_keep_the_sparkline_off_a_terminal() {
        let metrics = [bursts("0")];
        let groups = group(&metrics);

        let rows = summary_rows(&groups[0], &Glyphs::unicode(), None);

        assert_eq!("distribution", rows[0].last().map(String::as_str).unwrap());
    }

    #[test]
    fn test_summary_rows_have_no_sparkline_column_in_ascii() {
        let metrics = [bursts("0")];
        let groups = group(&metrics);

        let rows = summary_rows(&groups[0], &Glyphs::ascii(), None);

        assert_eq!(vec!["count", "p50", "p90", "p99"], rows[0]);
    }

    #[test]
    fn test_table_width_counts_padding_and_borders() {
        // " ab │ c " is eight columns wide.
        let rows = vec![
            vec!["ab".to_owned(), "c".to_owned()],
            vec!["a".to_owned(), "".to_owned()],
        ];

        assert_eq!(8, table_width(&rows));
    }

    #[test]
    fn test_natural_cmp_orders_digit_runs_as_numbers() {
        assert_eq!(Ordering::Less, natural_cmp("2", "10"));
        assert_eq!(Ordering::Less, natural_cmp("eth2", "eth10"));
        assert_eq!(Ordering::Equal, natural_cmp("eth10", "eth10"));
    }

    #[test]
    fn test_natural_cmp_falls_back_to_text_order() {
        assert_eq!(Ordering::Less, natural_cmp("Get", "Update"));
        assert_eq!(Ordering::Less, natural_cmp("eth", "eth1"));
    }
}
