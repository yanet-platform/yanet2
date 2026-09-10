//! Human-readable age and timestamp formatting shared across CLI crates.

use core::time::Duration;
use std::time::{SystemTime, UNIX_EPOCH};

use prost_types::Timestamp;

const SECONDS_PER_MINUTE: u64 = 60;
const SECONDS_PER_HOUR: u64 = 60 * SECONDS_PER_MINUTE;
const SECONDS_PER_DAY: u64 = 24 * SECONDS_PER_HOUR;

/// Formats a `Timestamp` as an age string relative to `now`.
///
/// Returns `None` when `ts` is absent or the zero sentinel, leaving the
/// caller to decide what sentinel to render. The output is capped at two
/// units because humantime's year/month decomposition would otherwise leave
/// a minute/second tail.
pub fn format_age(ts: Option<&Timestamp>, now: SystemTime) -> Option<String> {
    let ts = match ts {
        Some(ts) if ts.seconds != 0 || ts.nanos != 0 => ts,
        _ => return None,
    };

    Some(age_since(ts.seconds, now))
}

/// Formats the time elapsed since the Unix second `seconds` relative to
/// `now`, capped at two units like [`format_age`].
///
/// A negative second count reads as an age counted from the epoch.
pub fn age_since(seconds: i64, now: SystemTime) -> String {
    let now_secs = now.duration_since(UNIX_EPOCH).unwrap_or_default().as_secs();
    let ts_secs = seconds.max(0) as u64;
    let age = now_secs.saturating_sub(ts_secs);

    let rendered = humantime::format_duration(Duration::from_secs(round_age(age))).to_string();
    rendered.split_whitespace().take(2).collect::<Vec<_>>().join(" ")
}

/// The first second RFC 3339 cannot spell, midnight of the year 10000.
const RFC3339_LIMIT: i64 = 253_402_300_800;

/// Formats a `Timestamp` as an RFC 3339 instant in UTC, to the second.
///
/// Returns `None` when `ts` is absent, the zero sentinel, outside the
/// years 1970 to 9999 or malformed in its nanoseconds, leaving the caller
/// to decide what to render.
pub fn format_timestamp(ts: Option<&Timestamp>) -> Option<String> {
    let ts = match ts {
        Some(ts) if ts.seconds != 0 || ts.nanos != 0 => ts,
        _ => return None,
    };
    if !(0..RFC3339_LIMIT).contains(&ts.seconds) || !(0..1_000_000_000).contains(&ts.nanos) {
        return None;
    }
    let instant = UNIX_EPOCH + Duration::from_secs(u64::try_from(ts.seconds).ok()?);

    Some(humantime::format_rfc3339_seconds(instant).to_string())
}

/// Rounds an age in seconds down, dropping precision finer than the
/// displayed units.
///
/// Second-level precision is noise once an age grows large: seconds are
/// kept under a minute, dropped under a day, and minutes are dropped too
/// beyond a day.
fn round_age(seconds: u64) -> u64 {
    if seconds < SECONDS_PER_MINUTE {
        seconds
    } else if seconds < SECONDS_PER_DAY {
        seconds - seconds % SECONDS_PER_MINUTE
    } else {
        seconds - seconds % SECONDS_PER_HOUR
    }
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
    fn test_format_timestamp_renders_utc_to_the_second() {
        let epoch_minute = Timestamp { seconds: 60, nanos: 5 };

        assert_eq!(
            Some("1970-01-01T00:01:00Z".to_owned()),
            format_timestamp(Some(&epoch_minute))
        );
    }

    #[test]
    fn test_format_timestamp_none_for_absent_sentinel_and_unspellable() {
        for seconds in [-1, RFC3339_LIMIT, i64::MAX] {
            assert_eq!(
                None,
                format_timestamp(Some(&Timestamp { seconds, nanos: 0 })),
                "{seconds}"
            );
        }
        for nanos in [-1, 1_000_000_000] {
            assert_eq!(
                None,
                format_timestamp(Some(&Timestamp { seconds: 60, nanos })),
                "{nanos}"
            );
        }
        assert_eq!(None, format_timestamp(Some(&Timestamp { seconds: 0, nanos: 0 })));
        assert_eq!(None, format_timestamp(None));
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
}
