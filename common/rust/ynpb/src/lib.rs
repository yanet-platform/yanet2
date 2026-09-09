#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod pb {
    tonic::include_proto!("controlplane.ynpb.v1");
}

mod function;

/// Serializes a `BackendKind` discriminant as its lowercased short name
/// (e.g. `builtin`, `in_process`, `external`), or `unspecified` when unknown.
pub fn serialize_backend_kind<S>(value: &i32, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    let name = pb::BackendKind::try_from(*value)
        .unwrap_or_default()
        .as_str_name()
        .strip_prefix("BACKEND_KIND_")
        .unwrap_or("UNSPECIFIED")
        .to_lowercase();

    serializer.serialize_str(&name)
}

/// Returns the lowercase name of a logging-level wire value (e.g. `debug`,
/// `info`), or its decimal text when the value is unknown.
pub fn log_level_name(value: i32) -> String {
    match pb::LogLevel::try_from(value) {
        Ok(level) => level.as_str_name().to_lowercase(),
        Err(_) => value.to_string(),
    }
}

/// Serializes a logging-level wire value as its lowercase name, or as its
/// decimal text when the value is unknown.
pub fn serialize_log_level<S>(value: &i32, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    serializer.serialize_str(&log_level_name(*value))
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

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn test_log_level_name_known_value() {
        assert_eq!("debug", log_level_name(pb::LogLevel::Debug as i32));
    }

    #[test]
    fn test_log_level_name_unknown_discriminant() {
        assert_eq!("7", log_level_name(7));
    }
}
