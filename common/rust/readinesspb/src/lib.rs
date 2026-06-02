//! Compiled proto types for the shared readiness API.
//!
//! Exposes `pb::ReadyRequest`, `pb::ReadyResponse`, `pb::Scope`,
//! `pb::State`, and `pb::Reason` generated from
//! `common/readinesspb/readiness.proto`.

#[allow(clippy::all, non_snake_case)]
pub mod pb {
    tonic::include_proto!("readinesspb");
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
