//! Compiled proto types for the shared readiness API.
//!
//! Exposes `pb::ReadyRequest`, `pb::ReadyResponse`, `pb::Scope`,
//! `pb::State`, and `pb::Reason` generated from
//! `common/readinesspb/v1/readiness.proto`.

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod pb {
    tonic::include_proto!("common.readinesspb.v1");
}

/// Serializes a `readinesspb.State` discriminant as its lowercase name (e.g.
/// `"ready"`).
pub fn serialize_state<S>(value: &i32, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    let state = pb::State::try_from(*value).unwrap_or_default();

    commonpb::serde_with::lowercase_name(state.as_str_name(), "STATE_", serializer)
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
