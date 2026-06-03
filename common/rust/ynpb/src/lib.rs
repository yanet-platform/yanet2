#[allow(clippy::all, non_snake_case)]
pub mod pb {
    tonic::include_proto!("ynpb");
}

mod function;

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
