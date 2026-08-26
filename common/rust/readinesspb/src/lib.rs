//! Compiled proto types for the shared readiness API.
//!
//! Exposes `pb::ReadyRequest`, `pb::ReadyResponse`, `pb::Scope`,
//! `pb::State`, and `pb::Reason` generated from
//! `common/readinesspb/v1/readiness.proto`.

#[allow(clippy::all, clippy::std_instead_of_core, non_snake_case)]
pub mod pb {
    tonic::include_proto!("common.readinesspb.v1");
}

/// Serializes a `readinesspb.State` discriminant as its lowercase name (e.g.
/// `"ready"`).
pub fn serialize_state<S>(value: &i32, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    let name = pb::State::try_from(*value)
        .unwrap_or_default()
        .as_str_name()
        .strip_prefix("STATE_")
        .unwrap_or("unspecified")
        .to_lowercase();

    serializer.serialize_str(&name)
}

/// Serializes an `Option<prost_types::Timestamp>` as `{"seconds": i64, "nanos":
/// i32}` or `null` when absent.
pub fn serialize_timestamp<S>(value: &Option<prost_types::Timestamp>, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    use serde::Serialize;

    match value {
        Some(ts) => {
            #[derive(serde::Serialize)]
            struct Ts {
                seconds: i64,
                nanos: i32,
            }
            Ts { seconds: ts.seconds, nanos: ts.nanos }.serialize(serializer)
        }
        None => serializer.serialize_none(),
    }
}

/// Serializes an `Option<prost_types::Duration>` as
/// `{"seconds": i64, "nanos": i32}` or `null` when absent, keeping the
/// nanosecond part that a floating-point seconds value would lose.
pub fn serialize_duration<S>(value: &Option<prost_types::Duration>, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    use serde::Serialize;

    match value {
        Some(duration) => {
            #[derive(serde::Serialize)]
            struct Dur {
                seconds: i64,
                nanos: i32,
            }
            Dur {
                seconds: duration.seconds,
                nanos: duration.nanos,
            }
            .serialize(serializer)
        }
        None => serializer.serialize_none(),
    }
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn test_scope_json_serializes_expected_observation_interval() {
        let scope = pb::Scope {
            expected_observation_interval: Some(prost_types::Duration { seconds: 30, nanos: 123 }),
            ..Default::default()
        };

        let json = serde_json::to_value(scope).expect("readiness scope must serialize");

        assert_eq!(
            &serde_json::json!({"seconds": 30, "nanos": 123}),
            json.get("expected_observation_interval")
                .expect("duration field must be present")
        );
    }

    #[test]
    fn test_scope_json_serializes_absent_observation_interval_as_null() {
        let json = serde_json::to_value(pb::Scope::default()).expect("readiness scope must serialize");

        assert!(json["expected_observation_interval"].is_null());
    }
}
