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

    fn timestamp(seconds_ago: i64, now_secs: i64) -> Timestamp {
        Timestamp {
            seconds: now_secs - seconds_ago,
            nanos: 0,
        }
    }

    fn interval(seconds: i64) -> ProstDuration {
        ProstDuration { seconds, nanos: 0 }
    }

    #[test]
    fn test_is_stale_true_past_multiplied_contract() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let observed = timestamp(601, now_secs);

        // 300s contract, multiplier 2: the observation is past 600s.
        assert!(is_stale(Some(&observed), Some(&interval(300)), now, 2));
    }

    #[test]
    fn test_is_stale_false_within_multiplied_contract() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let observed = timestamp(599, now_secs);

        assert!(!is_stale(Some(&observed), Some(&interval(300)), now, 2));
    }

    #[test]
    fn test_is_stale_uses_own_contract_per_scope() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let observed = timestamp(130, now_secs);

        // The same age crosses a 30s contract at 4x but stays within a 5m
        // contract — the tag follows the scope's own cadence, not a global
        // constant.
        assert!(is_stale(Some(&observed), Some(&interval(30)), now, 4));
        assert!(!is_stale(Some(&observed), Some(&interval(300)), now, 4));
    }

    #[test]
    fn test_is_stale_honors_sub_second_precision() {
        // A fixed epoch-based clock, so the test controls the nanosecond
        // offset of `now` instead of inheriting `SystemTime::now`'s.
        let now = UNIX_EPOCH + Duration::new(10_000, 0);

        // Contract 1s, multiplier 2: the threshold is exactly 2s. An age of
        // 1.9s stays below it and an age of 2.1s crosses it — truncating
        // either timestamp to whole seconds would read both as 2s or 3s and
        // blur the boundary.
        let within = Timestamp { seconds: 9_998, nanos: 100_000_000 };
        let beyond = Timestamp { seconds: 9_997, nanos: 900_000_000 };

        assert!(!is_stale(Some(&within), Some(&interval(1)), now, 2));
        assert!(is_stale(Some(&beyond), Some(&interval(1)), now, 2));
    }

    #[test]
    fn test_is_stale_never_without_contract() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let observed = timestamp(10_000, now_secs);

        // A set-once scope publishes no contract and is never stale.
        assert!(!is_stale(Some(&observed), None, now, 3));
    }

    #[test]
    fn test_is_stale_false_when_observation_missing() {
        assert!(!is_stale(None, Some(&interval(30)), SystemTime::now(), 3));
    }

    #[test]
    fn test_is_stale_disabled_when_multiple_zero() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let observed = timestamp(10_000, now_secs);

        assert!(!is_stale(Some(&observed), Some(&interval(30)), now, 0));
    }

    #[test]
    fn test_is_stale_disabled_for_non_positive_contract() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let observed = timestamp(10_000, now_secs);

        // A zero or negative contract is the wire form of "no contract".
        assert!(!is_stale(Some(&observed), Some(&interval(0)), now, 3));
        assert!(!is_stale(
            Some(&observed),
            Some(&ProstDuration { seconds: -5, nanos: 0 }),
            now,
            3
        ));
    }
}
