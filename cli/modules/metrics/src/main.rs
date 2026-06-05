//! Generic metrics probe CLI.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::{GetMetricsRequest, GetMetricsResponse, Histogram, Label, Metric, metric::Value};
use tabled::{
    Table, Tabled,
    settings::{
        Color, Style,
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
    },
};
use ync::{
    client::ConnectionArgs,
    errors::Error,
    output::{self, CommonFormat},
};

/// Generic metrics probe — calls `GetMetrics` on any `MetricsService`.
///
/// Connects to the gateway and invokes `/<FQN>/GetMetrics` using tonic's
/// low-level dynamic dispatcher with the shared `commonpb` message types.
/// No per-service generated client is needed.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    /// Fully-qualified gRPC service name, e.g.
    /// `operators.route.operatorpb.v1.MetricsService`.
    pub name: String,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    match run(cmd).await {
        Ok(()) => {}
        Err(err) => {
            output::failure(&err);
            std::process::exit(err.exit_code());
        }
    }
}

/// Run the metrics probe.
async fn run(cmd: Cmd) -> Result<(), Error> {
    let response: GetMetricsResponse = ync::client::invoke_unary(
        &cmd.connection,
        "metrics",
        &cmd.name,
        "GetMetrics",
        GetMetricsRequest {},
    )
    .await?;

    let total = response.metrics.len();

    output::data(
        &response.metrics,
        response.metrics.is_empty(),
        format_args!("no metrics"),
        || {
            let mut scalars: Vec<&Metric> = response
                .metrics
                .iter()
                .filter(|m| !matches!(&m.value, Some(Value::Histogram(_))))
                .collect();
            scalars.sort_by(|a, b| a.name.cmp(&b.name));

            let mut histograms: Vec<&Metric> = response
                .metrics
                .iter()
                .filter(|m| matches!(&m.value, Some(Value::Histogram(_))))
                .collect();
            histograms.sort_by(|a, b| a.name.cmp(&b.name));

            if !scalars.is_empty() {
                let rows: Vec<MetricRow> = scalars.iter().map(|m| MetricRow::from(*m)).collect();
                print_metrics_table(rows);
            }

            if !histograms.is_empty() {
                println!();
                println!("Histograms");
                println!();

                for metric in &histograms {
                    if let Some(Value::Histogram(h)) = &metric.value {
                        print_histogram(&metric.name, &metric.labels, h);
                    }
                }
            }

            println!("summary: {total} metrics");
        },
    );

    Ok(())
}

/// A displayable row for the metrics table.
#[derive(Debug, Tabled)]
pub struct MetricRow {
    #[tabled(rename = "Name")]
    pub name: String,
    #[tabled(rename = "Labels")]
    pub labels: String,
    #[tabled(rename = "Type")]
    pub kind: String,
    #[tabled(rename = "Value")]
    pub value: String,
}

impl From<&Metric> for MetricRow {
    fn from(m: &Metric) -> Self {
        let labels = {
            let s = format_labels(&m.labels);
            if s.is_empty() { "-".to_string() } else { s }
        };

        let (kind, value) = match &m.value {
            Some(Value::Counter(c)) => ("counter".to_string(), c.to_string()),
            Some(Value::Gauge(g)) => ("gauge".to_string(), g.to_string()),
            Some(Value::Histogram(h)) => ("histogram".to_string(), format!("count={}", h.total_count)),
            None => ("unknown".to_string(), "-".to_string()),
        };

        Self {
            name: m.name.clone(),
            labels,
            kind,
            value,
        }
    }
}

fn print_metrics_table(rows: Vec<MetricRow>) {
    let mut table = Table::new(&rows);
    table.with(
        Style::modern()
            .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
            .remove_horizontal(),
    );

    if output::is_colored() {
        table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));
        table.modify(Rows::first(), Color::BOLD);
    }

    ync::display::fit_terminal_width(&mut table);
    println!("{table}");
}

/// Returns the `k=v, k=v` join of `labels`, or an empty string when `labels`
/// is empty.
fn format_labels(labels: &[Label]) -> String {
    labels
        .iter()
        .map(|l| format!("{}={}", l.name, l.value))
        .collect::<Vec<_>>()
        .join(", ")
}

/// Formats `value` as a human-readable bound string.
///
/// `+Inf`/`-Inf` become `"inf"`/`"-inf"`, whole numbers become their integer
/// form, and all other values use the default `f64` display.
fn format_bound(value: f64) -> String {
    if value.is_infinite() {
        if value.is_sign_negative() {
            return "-inf".to_string();
        }

        return "inf".to_string();
    }

    if value.fract() == 0.0 {
        return format!("{}", value as i64);
    }

    format!("{value}")
}

/// Returns the bar length for a bucket, scaled to `BAR_MAX`.
///
/// Returns `0` when `max_count` is `0`. Non-zero counts that round to `0`
/// are bumped to `1` so every populated bucket shows at least one bar
/// character.
fn bar_len(count: u64, max_count: u64) -> usize {
    const BAR_MAX: usize = 20;

    if max_count == 0 {
        return 0;
    }

    let mut n = ((count as f64 / max_count as f64) * BAR_MAX as f64).round() as usize;

    if count > 0 && n == 0 {
        n = 1;
    }

    n
}

/// Prints a single histogram block to stdout.
fn print_histogram(name: &str, labels: &[Label], histogram: &Histogram) {
    let label_str = format_labels(labels);
    if label_str.is_empty() {
        println!("{name}");
    } else {
        println!("{name} {{{label_str}}}");
    }

    let buckets = &histogram.buckets;

    if buckets.is_empty() {
        println!("  count = {}", histogram.total_count);
        println!();
        return;
    }

    let max_count = buckets.iter().map(|b| b.count).max().unwrap_or(0);

    let bounds: Vec<(String, String)> = buckets
        .iter()
        .enumerate()
        .map(|(idx, bucket)| {
            let lower = if idx == 0 { 0.0 } else { buckets[idx - 1].upper_bound };
            (format_bound(lower), format_bound(bucket.upper_bound))
        })
        .collect();

    let wl = bounds.iter().map(|(l, _)| l.len()).max().unwrap_or(0);
    let wu = bounds.iter().map(|(_, u)| u.len()).max().unwrap_or(0);
    let wc = buckets.iter().map(|b| b.count.to_string().len()).max().unwrap_or(0);

    for (bucket, (lower, upper)) in buckets.iter().zip(bounds.iter()) {
        let bars = "∎".repeat(bar_len(bucket.count, max_count));
        let count = bucket.count;
        println!("  {lower:>wl$} .. {upper:>wu$} [ {count:>wc$} ] {bars}");
    }

    println!("  count = {}", histogram.total_count);
    println!();
}

#[cfg(test)]
mod test {
    use super::bar_len;

    #[test]
    fn bar_len_scaling() {
        // Full scale.
        assert_eq!(20, bar_len(310, 310));
        // Partial scale.
        assert_eq!(3, bar_len(45, 310));
        // Zero count produces zero bars.
        assert_eq!(0, bar_len(0, 310));
    }

    #[test]
    fn bar_len_edge_cases() {
        // Non-zero count that rounds below 1 is bumped to 1.
        assert_eq!(1, bar_len(1, 1000));
        // Zero max guard — never divide by zero.
        assert_eq!(0, bar_len(5, 0));
    }
}
