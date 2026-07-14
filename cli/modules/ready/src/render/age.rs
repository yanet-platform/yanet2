//! Age formatting and the staleness predicate.

use core::time::Duration;
use std::time::{SystemTime, UNIX_EPOCH};

use prost_types::Timestamp;

const SECONDS_PER_MINUTE: u64 = 60;
const SECONDS_PER_HOUR: u64 = 60 * SECONDS_PER_MINUTE;
const SECONDS_PER_DAY: u64 = 24 * SECONDS_PER_HOUR;

/// Formats a `Timestamp` as an age string relative to `now`.
///
/// Returns `None` when `ts` is absent or the zero sentinel, signalling the
/// caller should render the literal `never` instead. The output is capped
/// at two units because humantime's year/month decomposition would
/// otherwise leave a minute/second tail.
pub fn format_age(ts: Option<&Timestamp>, now: SystemTime) -> Option<String> {
    let ts = match ts {
        Some(ts) if ts.seconds != 0 || ts.nanos != 0 => ts,
        _ => return None,
    };

    let now_secs = now.duration_since(UNIX_EPOCH).unwrap_or_default().as_secs();
    let ts_secs = ts.seconds.max(0) as u64;
    let age = now_secs.saturating_sub(ts_secs);

    let rendered = humantime::format_duration(Duration::from_secs(round_age(age))).to_string();
    Some(rendered.split_whitespace().take(2).collect::<Vec<_>>().join(" "))
}

/// Rounds an age in seconds down, dropping precision finer than the
/// displayed units.
///
/// Second-level precision is noise in a readiness view: seconds are kept
/// under a minute, dropped under a day, and minutes are dropped too beyond
/// a day.
fn round_age(seconds: u64) -> u64 {
    if seconds < SECONDS_PER_MINUTE {
        seconds
    } else if seconds < SECONDS_PER_DAY {
        seconds - seconds % SECONDS_PER_MINUTE
    } else {
        seconds - seconds % SECONDS_PER_HOUR
    }
}

/// Returns whether a scope's staleness tag should be shown.
///
/// True only when the scope has been re-observed since its last transition
/// and that observation is older than `stale_after`. A scope set once and
/// never re-observed is never stale; `stale_after == Duration::ZERO`
/// disables the tag unconditionally.
pub fn is_stale(
    observed_at: Option<&Timestamp>,
    last_transition_time: Option<&Timestamp>,
    now: SystemTime,
    stale_after: Duration,
) -> bool {
    if stale_after.is_zero() {
        return false;
    }

    let (Some(observed_at), Some(last_transition_time)) = (observed_at, last_transition_time) else {
        return false;
    };

    if (observed_at.seconds, observed_at.nanos) <= (last_transition_time.seconds, last_transition_time.nanos) {
        return false;
    }

    let now_secs = now.duration_since(UNIX_EPOCH).unwrap_or_default().as_secs();
    let observed_secs = observed_at.seconds.max(0) as u64;
    let age = Duration::from_secs(now_secs.saturating_sub(observed_secs));

    age > stale_after
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn round_age_sub_minute_keeps_seconds() {
        assert_eq!(0, round_age(0));
        assert_eq!(45, round_age(45));
        assert_eq!(59, round_age(59));
    }

    #[test]
    fn round_age_minute_band_drops_seconds() {
        assert_eq!(60, round_age(60));
        assert_eq!(12 * SECONDS_PER_MINUTE, round_age(12 * SECONDS_PER_MINUTE + 34));
        assert_eq!(
            23 * SECONDS_PER_HOUR + 59 * SECONDS_PER_MINUTE,
            round_age(SECONDS_PER_DAY - 1)
        );
    }

    #[test]
    fn round_age_day_band_rounds_to_hours() {
        assert_eq!(
            3 * SECONDS_PER_DAY + 5 * SECONDS_PER_HOUR,
            round_age(3 * SECONDS_PER_DAY + 5 * SECONDS_PER_HOUR + 40 * SECONDS_PER_MINUTE)
        );
        assert_eq!(SECONDS_PER_DAY, round_age(SECONDS_PER_DAY));
    }

    #[test]
    fn format_age_none_returns_none() {
        assert_eq!(None, format_age(None, SystemTime::now()));
    }

    #[test]
    fn format_age_zero_sentinel_returns_none() {
        let ts = Timestamp { seconds: 0, nanos: 0 };
        assert_eq!(None, format_age(Some(&ts), SystemTime::now()));
    }

    #[test]
    fn format_age_future_timestamp_saturates_to_zero() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let ts = Timestamp { seconds: now_secs + 60, nanos: 0 };

        assert_eq!(Some("0s".to_string()), format_age(Some(&ts), now));
    }

    #[test]
    fn format_age_large_age_caps_at_two_units() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let ts = Timestamp {
            seconds: now_secs - 400 * SECONDS_PER_DAY as i64,
            nanos: 0,
        };

        let rendered = format_age(Some(&ts), now).unwrap();

        assert!(rendered.split_whitespace().count() <= 2);
    }

    #[test]
    fn is_stale_disabled_when_stale_after_zero() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let old = Timestamp { seconds: now_secs - 10_000, nanos: 0 };
        let recent = Timestamp { seconds: now_secs, nanos: 0 };

        assert!(!is_stale(Some(&old), Some(&recent), now, Duration::ZERO));
    }

    #[test]
    fn is_stale_never_for_set_once_scope() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let ts = Timestamp { seconds: now_secs - 10_000, nanos: 0 };

        assert!(!is_stale(Some(&ts), Some(&ts), now, Duration::from_secs(60)));
    }

    #[test]
    fn is_stale_true_past_threshold() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let transition = Timestamp { seconds: now_secs - 10_000, nanos: 0 };
        let observed = Timestamp { seconds: now_secs - 120, nanos: 0 };

        assert!(is_stale(
            Some(&observed),
            Some(&transition),
            now,
            Duration::from_secs(60)
        ));
    }

    #[test]
    fn is_stale_false_within_threshold() {
        let now = SystemTime::now();
        let now_secs = now.duration_since(UNIX_EPOCH).unwrap().as_secs() as i64;
        let transition = Timestamp { seconds: now_secs - 10_000, nanos: 0 };
        let observed = Timestamp { seconds: now_secs - 10, nanos: 0 };

        assert!(!is_stale(
            Some(&observed),
            Some(&transition),
            now,
            Duration::from_secs(60)
        ));
    }

    #[test]
    fn is_stale_false_when_timestamps_missing() {
        assert!(!is_stale(None, None, SystemTime::now(), Duration::from_secs(60)));
    }
}
