//! Shared metrics rendering for module CLIs.
//!
//! Converts a `GetMetricsResponse` into the domain `Metric` type, formats
//! gauge/histogram values for human display, and renders the `GRPC CALLS` /
//! `GRPC HANDLING LATENCIES` sections shared by every module's `metrics`
//! subcommand. Module-specific grouping (per-module counter tables) stays in
//! each CLI, since the label sets and grouping keys differ per module.

use std::collections::HashMap;

use commonpb::pb::metric::Value;
use serde::Serialize;
use tabled::Tabled;

use crate::display::print_table_from_entries;

#[derive(Serialize, Clone, PartialEq)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    Counter,
    Gauge,
    Histogram,
    Unknown,
}

#[derive(Serialize)]
pub struct Bucket {
    pub upper_bound: f64,
    pub count: u64,
}

#[derive(Serialize)]
pub struct Histogram {
    pub total_count: u64,
    pub buckets: Vec<Bucket>,
}

#[derive(Serialize)]
pub struct Label {
    pub name: String,
    pub value: String,
}

#[derive(Serialize)]
pub struct Metric {
    pub name: String,
    pub labels: Vec<Label>,
    pub kind: Kind,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub value: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub histogram: Option<Histogram>,
}

impl Metric {
    pub fn from_proto(m: commonpb::pb::Metric) -> Self {
        let labels = m
            .labels
            .into_iter()
            .map(|l| Label { name: l.name, value: l.value })
            .collect();

        match m.value {
            Some(Value::Counter(c)) => Self {
                name: m.name,
                labels,
                kind: Kind::Counter,
                value: Some(c as f64),
                histogram: None,
            },
            Some(Value::Gauge(g)) => Self {
                name: m.name,
                labels,
                kind: Kind::Gauge,
                value: Some(g),
                histogram: None,
            },
            Some(Value::Histogram(h)) => Self {
                name: m.name,
                labels,
                kind: Kind::Histogram,
                value: None,
                histogram: Some(Histogram {
                    total_count: h.total_count,
                    buckets: h
                        .buckets
                        .into_iter()
                        .map(|b| Bucket {
                            upper_bound: b.upper_bound,
                            count: b.count,
                        })
                        .collect(),
                }),
            },
            None => Self {
                name: m.name,
                labels,
                kind: Kind::Unknown,
                value: None,
                histogram: None,
            },
        }
    }

    pub fn label_value(&self, key: &str) -> Option<&str> {
        self.labels.iter().find(|l| l.name == key).map(|l| l.value.as_str())
    }
}

/// Computes the p-th percentile bucket label for a histogram whose upper
/// bounds are in seconds.
pub fn histogram_percentile(buckets: &[Bucket], total: u64, p: f64) -> String {
    if total == 0 || buckets.is_empty() {
        return "-".to_string();
    }
    let target = ((total as f64 * p / 100.0).ceil() as u64).max(1);
    let mut cumulative: u64 = 0;
    for (i, b) in buckets.iter().enumerate() {
        cumulative = cumulative.saturating_add(b.count);
        if cumulative >= target {
            return if b.upper_bound.is_infinite() {
                // Report the last finite bound as the lower edge of the
                // overflow bucket. When the +Inf bucket is the very first one
                // there is no finite predecessor to reference.
                match buckets[..i].iter().rev().find(|bucket| bucket.upper_bound.is_finite()) {
                    Some(prev) => format!(">{:.3}s", prev.upper_bound),
                    None => "+Inf".to_string(),
                }
            } else {
                format!("≤{:.3}s", b.upper_bound)
            };
        }
    }
    "-".to_string()
}

/// Formats `n` with thousands separators, e.g. `1234567` -> `1,234,567`.
pub fn format_number(n: u64) -> String {
    let s = n.to_string();
    let mut result = String::new();
    for (i, c) in s.chars().rev().enumerate() {
        if i > 0 && i % 3 == 0 {
            result.push(',');
        }
        result.push(c);
    }
    result.chars().rev().collect()
}

/// Formats a gauge `value` for `name`, picking a unit from the metric name's
/// suffix.
///
/// `_ns` names scale through ns/µs/ms/s, `_bytes` names scale through
/// B/KiB/MiB/GiB, anything else falls back to `format_number`.
pub fn format_gauge_value(name: &str, value: f64) -> String {
    if name.ends_with("_ns") {
        if value < 1_000.0 {
            format!("{:.0}ns", value)
        } else if value < 1_000_000.0 {
            format!("{:.2}µs", value / 1_000.0)
        } else if value < 1_000_000_000.0 {
            format!("{:.2}ms", value / 1_000_000.0)
        } else {
            format!("{:.2}s", value / 1_000_000_000.0)
        }
    } else if name.ends_with("_bytes") {
        if value < 1024.0 {
            format!("{:.0} B", value)
        } else if value < 1024.0 * 1024.0 {
            format!("{:.2} KiB", value / 1024.0)
        } else if value < 1024.0 * 1024.0 * 1024.0 {
            format!("{:.2} MiB", value / (1024.0 * 1024.0))
        } else {
            format!("{:.2} GiB", value / (1024.0 * 1024.0 * 1024.0))
        }
    } else {
        format_number(value as u64)
    }
}

/// Turns a `snake_case` metric name into a `Title Case` display name, after
/// stripping `prefix` (e.g. `"acl_"` or `"fwstate_"`) if present.
pub fn metric_display_name(name: &str, prefix: &str) -> String {
    let stripped = name.strip_prefix(prefix).unwrap_or(name);
    stripped
        .split('_')
        .map(|word| {
            let mut c = word.chars();
            match c.next() {
                None => String::new(),
                Some(first) => first.to_uppercase().collect::<String>() + c.as_str(),
            }
        })
        .collect::<Vec<_>>()
        .join(" ")
}

#[derive(Tabled)]
pub struct GaugeRow {
    #[tabled(rename = "Metric")]
    pub metric: String,
    #[tabled(rename = "Value")]
    pub value: String,
}

#[derive(Tabled)]
struct GrpcCallRow {
    #[tabled(rename = "Method")]
    method: String,
    #[tabled(rename = "Code")]
    code: String,
    #[tabled(rename = "Handled")]
    handled: String,
}

#[derive(Tabled)]
struct GrpcLatRow {
    #[tabled(rename = "Method")]
    method: String,
    #[tabled(rename = "Total Calls")]
    total: String,
    #[tabled(rename = "P50")]
    p50: String,
    #[tabled(rename = "P95")]
    p95: String,
    #[tabled(rename = "P99")]
    p99: String,
}

/// Prints the `GRPC CALLS` and `GRPC HANDLING LATENCIES` sections shared by
/// every module's `metrics` subcommand.
///
/// `grpc_counters` are the `grpc_*` metrics of `Kind::Counter` and
/// `grpc_histograms` the `grpc_*` metrics of `Kind::Histogram`; callers
/// collect these while walking their full metric list, since the split
/// between gRPC metrics and module-specific metrics differs per module.
pub fn print_grpc_metrics(grpc_counters: &[&Metric], grpc_histograms: &[&Metric]) {
    if !grpc_counters.is_empty() {
        // Collect started counts keyed by grpc_method.
        let mut started: HashMap<String, u64> = HashMap::new();
        // Collect handled counts keyed by (grpc_method, grpc_code), preserving order.
        let mut handled_keys: Vec<(String, String)> = Vec::new();
        let mut handled: HashMap<(String, String), u64> = HashMap::new();

        for m in grpc_counters {
            let method = m.label_value("grpc_method").unwrap_or("").to_string();
            if m.name == "grpc_server_started_total" {
                let count = m.value.unwrap_or(0.0) as u64;
                *started.entry(method).or_default() += count;
            } else if m.name == "grpc_server_handled_total" {
                let code = m.label_value("grpc_code").unwrap_or("").to_string();
                let key = (method, code);
                if !handled.contains_key(&key) {
                    handled_keys.push(key.clone());
                }
                *handled.entry(key).or_default() += m.value.unwrap_or(0.0) as u64;
            }
        }

        if !handled_keys.is_empty() || !started.is_empty() {
            println!();
            println!("GRPC CALLS");
            println!();
        }

        if !handled_keys.is_empty() {
            let rows: Vec<GrpcCallRow> = handled_keys
                .iter()
                .map(|(method, code)| GrpcCallRow {
                    method: method.clone(),
                    code: code.clone(),
                    handled: format_number(handled[&(method.clone(), code.clone())]),
                })
                .collect();
            print_table_from_entries(rows);
        }

        if !started.is_empty() {
            println!();
            let mut started_methods: Vec<&String> = started.keys().collect();
            started_methods.sort();
            for method in started_methods {
                println!("  started  {method}: {}", format_number(started[method]));
            }
        }
    }

    if !grpc_histograms.is_empty() {
        println!();
        println!("GRPC HANDLING LATENCIES");
        println!();
        let rows: Vec<GrpcLatRow> = grpc_histograms
            .iter()
            .map(|m| {
                let method = m.label_value("grpc_method").unwrap_or("unknown").to_string();
                match &m.histogram {
                    Some(h) => GrpcLatRow {
                        method,
                        total: format_number(h.total_count),
                        p50: histogram_percentile(&h.buckets, h.total_count, 50.0),
                        p95: histogram_percentile(&h.buckets, h.total_count, 95.0),
                        p99: histogram_percentile(&h.buckets, h.total_count, 99.0),
                    },
                    None => GrpcLatRow {
                        method,
                        total: "-".into(),
                        p50: "-".into(),
                        p95: "-".into(),
                        p99: "-".into(),
                    },
                }
            })
            .collect();
        print_table_from_entries(rows);
    }
}

#[cfg(test)]
mod test {
    use super::*;

    fn bucket(upper_bound: f64, count: u64) -> Bucket {
        Bucket { upper_bound, count }
    }

    #[test]
    fn percentile_empty_or_zero_total() {
        assert_eq!("-", histogram_percentile(&[], 0, 50.0));
        assert_eq!("-", histogram_percentile(&[bucket(1.0, 0)], 0, 50.0));
    }

    #[test]
    fn percentile_finite_bucket() {
        let buckets = [bucket(0.001, 5), bucket(0.01, 5), bucket(f64::INFINITY, 0)];
        // total=10, p50 => target=5, cumulative reaches 5 in the first bucket.
        assert_eq!("≤0.001s", histogram_percentile(&buckets, 10, 50.0));
        // p95 => target=ceil(9.5)=10, reached in the second bucket.
        assert_eq!("≤0.010s", histogram_percentile(&buckets, 10, 95.0));
    }

    #[test]
    fn percentile_overflow_reports_last_finite_bound() {
        let buckets = [bucket(0.001, 1), bucket(0.01, 1), bucket(f64::INFINITY, 8)];
        // p99 => target=ceil(9.9)=10, only reached in the +Inf bucket.
        assert_eq!(">0.010s", histogram_percentile(&buckets, 10, 99.0));
    }

    #[test]
    fn percentile_single_infinite_bucket() {
        // A histogram with only the +Inf bucket has no finite predecessor;
        // it must not panic and should fall back to "+Inf".
        let buckets = [bucket(f64::INFINITY, 4)];
        assert_eq!("+Inf", histogram_percentile(&buckets, 4, 50.0));
    }
}
