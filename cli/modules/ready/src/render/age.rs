//! The staleness predicate.

use core::time::Duration;
use std::time::{SystemTime, UNIX_EPOCH};

use prost_types::Timestamp;

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
