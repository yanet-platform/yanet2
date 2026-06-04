#[allow(clippy::all, non_snake_case)]
pub mod pb {
    tonic::include_proto!("ynpb");
}

mod function;

/// Serializes a `ynpb.BackendKind` discriminant as its lowercase short name
/// (e.g. `"in_process"`, `"out_of_process"`, `"unspecified"`).
///
/// The `BACKEND_KIND_` prefix is stripped and the remainder lowercased. Unknown
/// discriminants fall back to `"unspecified"`.
pub fn serialize_backend_kind<S>(value: &i32, serializer: S) -> Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    let name = pb::BackendKind::try_from(*value)
        .unwrap_or_default()
        .as_str_name()
        .strip_prefix("BACKEND_KIND_")
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
