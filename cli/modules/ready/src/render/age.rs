//! The staleness predicate.

use core::time::Duration;
use std::time::{SystemTime, UNIX_EPOCH};

use prost_types::{Duration as ProstDuration, Timestamp};

/// Returns whether a scope's staleness tag should be shown.
///
/// A scope with an observation contract goes stale once the time since its
/// last observation exceeds the contract multiplied by `multiple`. A scope
/// without a contract has no natural heartbeat and is never judged stale.
pub fn is_stale(
    observed_at: Option<&Timestamp>,
    expected_interval: Option<&ProstDuration>,
    now: SystemTime,
    multiple: u32,
) -> bool {
    let Some(expected_interval) = expected_interval else {
        return false;
    };

    let Some(observed_at) = observed_at else {
        return false;
    };

    let threshold = observation_threshold(expected_interval, multiple);
    if threshold.is_zero() {
        return false;
    }

    // Full nanosecond timestamps on both sides: a threshold can land
    // between two whole seconds, so truncating either timestamp to seconds
    // could push an age across it early. The clock's u128 nanoseconds
    // saturate into i128 rather than truncating, so the arithmetic stays
    // valid past u64's nanosecond horizon.
    let now_nanos = i128::try_from(now.duration_since(UNIX_EPOCH).unwrap_or_default().as_nanos()).unwrap_or(i128::MAX);
    let observed_nanos = i128::from(observed_at.seconds)
        .saturating_mul(1_000_000_000)
        .saturating_add(i128::from(observed_at.nanos));
    let age_nanos = now_nanos.saturating_sub(observed_nanos).max(0);
    let age = Duration::from_nanos(u64::try_from(age_nanos).unwrap_or(u64::MAX));

    age > threshold
}

/// Converts an observation contract and its multiplier into the staleness
/// threshold.
///
/// A contract that resolves to a non-positive span yields a zero threshold,
/// which `is_stale` treats as "never stale".
fn observation_threshold(expected_interval: &ProstDuration, multiple: u32) -> Duration {
    let seconds = expected_interval.seconds;
    let nanos = expected_interval.nanos;
    if seconds < 0 || nanos < 0 {
        return Duration::ZERO;
    }

    // Exact integer arithmetic in nanoseconds with saturation: no rounding
    // and no overflow for any realistic contract and multiplier.
    let total_nanos = (seconds as i128)
        .saturating_mul(1_000_000_000)
        .saturating_add(i128::from(nanos));
    let threshold_nanos = total_nanos.saturating_mul(i128::from(multiple));
    if threshold_nanos <= 0 || threshold_nanos > u64::MAX as i128 {
        return Duration::ZERO;
    }

    Duration::from_nanos(threshold_nanos as u64)
}

#[cfg(test)]
mod test {
    use super::*;

    fn now() -> SystemTime {
        UNIX_EPOCH + Duration::from_secs(10_000)
    }

    fn observed_at(age: Duration) -> Timestamp {
        let observed = now().duration_since(UNIX_EPOCH).unwrap() - age;
        Timestamp {
            seconds: observed.as_secs() as i64,
            nanos: observed.subsec_nanos() as i32,
        }
    }

    fn interval(seconds: i64) -> ProstDuration {
        ProstDuration { seconds, nanos: 0 }
    }

    #[test]
    fn test_is_stale_requires_age_strictly_past_exact_boundary() {
        let boundary = observed_at(Duration::from_secs(600));
        let past = observed_at(Duration::from_nanos(600_000_000_001));

        assert!(!is_stale(Some(&boundary), Some(&interval(300)), now(), 2));
        assert!(is_stale(Some(&past), Some(&interval(300)), now(), 2));
    }

    #[test]
    fn test_is_stale_uses_distinct_interval_per_scope() {
        let observed = observed_at(Duration::from_secs(130));

        assert!(is_stale(Some(&observed), Some(&interval(30)), now(), 4));
        assert!(!is_stale(Some(&observed), Some(&interval(300)), now(), 4));
    }

    #[test]
    fn test_is_stale_false_without_observation_contract() {
        let observed = observed_at(Duration::from_secs(1_000));

        assert!(!is_stale(Some(&observed), None, now(), 3));
    }

    #[test]
    fn test_is_stale_false_without_observation() {
        assert!(!is_stale(None, Some(&interval(30)), now(), 3));
    }

    #[test]
    fn test_is_stale_false_when_multiplier_is_zero() {
        let observed = observed_at(Duration::from_secs(1_000));

        assert!(!is_stale(Some(&observed), Some(&interval(30)), now(), 0));
    }

    #[test]
    fn test_is_stale_preserves_nanosecond_timestamp_precision() {
        let within = observed_at(Duration::from_nanos(1_999_999_999));
        let past = observed_at(Duration::from_nanos(2_000_000_001));

        assert!(!is_stale(Some(&within), Some(&interval(1)), now(), 2));
        assert!(is_stale(Some(&past), Some(&interval(1)), now(), 2));
    }
}
