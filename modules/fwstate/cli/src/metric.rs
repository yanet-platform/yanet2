use commonpb::pb;
use serde::Serialize;

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
    pub fn from_proto(m: pb::Metric) -> Self {
        use pb::metric::Value;

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
                match buckets[..i].iter().rev().find(|p| p.upper_bound.is_finite()) {
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

#[cfg(test)]
mod tests {
    use super::*;

    fn bucket(upper_bound: f64, count: u64) -> Bucket {
        Bucket { upper_bound, count }
    }

    #[test]
    fn percentile_empty_or_zero_total() {
        assert_eq!(histogram_percentile(&[], 0, 50.0), "-");
        assert_eq!(histogram_percentile(&[bucket(1.0, 0)], 0, 50.0), "-");
    }

    #[test]
    fn percentile_finite_bucket() {
        let buckets = [bucket(0.001, 5), bucket(0.01, 5), bucket(f64::INFINITY, 0)];
        // total=10, p50 => target=5, cumulative reaches 5 in the first bucket.
        assert_eq!(histogram_percentile(&buckets, 10, 50.0), "≤0.001s");
        // p95 => target=ceil(9.5)=10, reached in the second bucket.
        assert_eq!(histogram_percentile(&buckets, 10, 95.0), "≤0.010s");
    }

    #[test]
    fn percentile_overflow_reports_last_finite_bound() {
        let buckets = [bucket(0.001, 1), bucket(0.01, 1), bucket(f64::INFINITY, 8)];
        // p99 => target=ceil(9.9)=10, only reached in the +Inf bucket.
        assert_eq!(histogram_percentile(&buckets, 10, 99.0), ">0.010s");
    }

    #[test]
    fn percentile_single_infinite_bucket() {
        // A histogram with only the +Inf bucket has no finite predecessor;
        // it must not panic and should fall back to "+Inf".
        let buckets = [bucket(f64::INFINITY, 4)];
        assert_eq!(histogram_percentile(&buckets, 4, 50.0), "+Inf");
    }
}
