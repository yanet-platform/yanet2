//! Shared metrics domain types for module CLIs.
//!
//! The domain `Metric` mirrors the wire message for display code. Grouping
//! and rendering stay in each CLI, since label sets and grouping keys differ.

use commonpb::pb::metric::Value;
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
    pub fn from_proto(m: commonpb::pb::Metric) -> Self {
        let kind = proto_kind(&m);
        let labels = m
            .labels
            .into_iter()
            .map(|l| Label { name: l.name, value: l.value })
            .collect();

        match m.value {
            Some(Value::Counter(c)) => Self {
                name: m.name,
                labels,
                kind,
                value: Some(c as f64),
                histogram: None,
            },
            Some(Value::Gauge(g)) => Self {
                name: m.name,
                labels,
                kind,
                value: Some(g),
                histogram: None,
            },
            Some(Value::Histogram(h)) => Self {
                name: m.name,
                labels,
                kind,
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
                kind,
                value: None,
                histogram: None,
            },
        }
    }

    pub fn label_value(&self, key: &str) -> Option<&str> {
        self.labels.iter().find(|l| l.name == key).map(|l| l.value.as_str())
    }
}

/// Returns the [`Kind`] of a wire metric from its `value` oneof.
pub fn proto_kind(m: &commonpb::pb::Metric) -> Kind {
    match &m.value {
        Some(Value::Counter(_)) => Kind::Counter,
        Some(Value::Gauge(_)) => Kind::Gauge,
        Some(Value::Histogram(_)) => Kind::Histogram,
        None => Kind::Unknown,
    }
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

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn proto_kind_matches_from_proto_mapping() {
        let wire = |value| commonpb::pb::Metric {
            name: "m".to_string(),
            labels: Vec::new(),
            value,
        };
        let cases = [
            (wire(Some(Value::Counter(1))), Kind::Counter),
            (wire(Some(Value::Gauge(1.0))), Kind::Gauge),
            (
                wire(Some(Value::Histogram(commonpb::pb::Histogram {
                    buckets: Vec::new(),
                    total_count: 0,
                }))),
                Kind::Histogram,
            ),
            (wire(None), Kind::Unknown),
        ];

        for (m, expected) in cases {
            assert!(proto_kind(&m) == expected);
            assert!(Metric::from_proto(m).kind == expected);
        }
    }
}
