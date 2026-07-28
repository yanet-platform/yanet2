//! JSON payload for `fib show --format json`.
//!
//! Each record carries `start`/`end`, or an `invalid` marker for a range
//! that failed to parse, plus `nexthops` -- always an array mirroring the
//! wire entry regardless of whether the range parsed, empty rather than
//! absent for a record with none.

use serde::Serialize;

use super::{format_mac, FibRecord, RangeUnit};
use crate::routepb::FibNexthop;

/// One nexthop, as serialized in a [`FibRecordJson`].
#[derive(Debug, Serialize)]
pub struct FibNexthopJson {
    pub dst_mac: String,
    pub src_mac: String,
    pub device: String,
}

impl From<&FibNexthop> for FibNexthopJson {
    fn from(nexthop: &FibNexthop) -> Self {
        Self {
            dst_mac: format_mac(nexthop.dst_mac),
            src_mac: format_mac(nexthop.src_mac),
            device: nexthop.device.clone(),
        }
    }
}

/// JSON range representation for one FIB record.
///
/// A well-formed range carries `start`/`end`; an invalid one carries
/// neither, just an honest `invalid` marker, rather than fabricating
/// endpoints for a range that failed to parse.
#[derive(Debug, Serialize)]
#[serde(untagged)]
pub enum FibRangeJson {
    Range { start: String, end: String },
    Invalid { invalid: bool },
}

impl From<RangeUnit> for FibRangeJson {
    fn from(unit: RangeUnit) -> Self {
        match unit {
            RangeUnit::Range { start, end } => Self::Range {
                start: start.to_string(),
                end: end.to_string(),
            },
            RangeUnit::Invalid => Self::Invalid { invalid: true },
        }
    }
}

/// One FIB record, as serialized for `--format json`.
///
/// `nexthops` mirrors the wire entry regardless of whether `range`
/// serializes as `start`/`end` or as `invalid: true` -- a malformed,
/// absent, or inverted range still carries whatever nexthops the server
/// attached to it. A record with no nexthops still serializes with
/// `nexthops: []` rather than disappearing.
#[derive(Debug, Serialize)]
pub struct FibRecordJson {
    #[serde(flatten)]
    pub range: FibRangeJson,
    pub nexthops: Vec<FibNexthopJson>,
}

impl From<&FibRecord> for FibRecordJson {
    fn from(record: &FibRecord) -> Self {
        Self {
            range: FibRangeJson::from(record.range),
            nexthops: record.nexthops.iter().map(FibNexthopJson::from).collect(),
        }
    }
}
