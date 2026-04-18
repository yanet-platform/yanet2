use serde::Serialize;

use crate::commonpb;

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
    pub fn from_proto(m: commonpb::Metric) -> Self {
        use commonpb::metric::Value;

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

pub fn histogram_percentile(buckets: &[Bucket], total: u64, p: f64) -> String {
    if total == 0 || buckets.is_empty() {
        return "-".to_string();
    }
    let target = ((total as f64 * p / 100.0).ceil() as u64).max(1);
    for b in buckets {
        if b.count >= target {
            return if b.upper_bound.is_infinite() {
                format!(">{}ms", buckets[buckets.len().saturating_sub(2)].upper_bound as u64)
            } else {
                format!("≤{}ms", b.upper_bound as u64)
            };
        }
    }
    "-".to_string()
}
