use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client(
        "../../..",
        &[
            "controlplane/ynpb/v1/logging.proto",
            "controlplane/ynpb/v1/device.proto",
            "controlplane/ynpb/v1/function.proto",
            "controlplane/ynpb/v1/pipeline.proto",
            "controlplane/ynpb/v1/inspect.proto",
            "controlplane/ynpb/v1/memory.proto",
            "controlplane/ynpb/v1/counters.proto",
            "controlplane/ynpb/v1/gateway.proto",
            "controlplane/ynpb/v1/auth.proto",
        ],
    )
    .runtime("::ync_rpc")
    .serialize()
    .with(|builder| {
        builder
            .field_attribute(
                ".controlplane.ynpb.v1.RegisteredBackend.last_seen_at",
                "#[serde(serialize_with = \"commonpb::serde_with::timestamp\")]",
            )
            .field_attribute(
                ".controlplane.ynpb.v1.RegisteredBackend.kind",
                "#[serde(serialize_with = \"crate::serialize_backend_kind\")]",
            )
            .field_attribute(
                ".controlplane.ynpb.v1.GetLevelResponse.level",
                "#[serde(serialize_with = \"crate::serialize_log_level\")]",
            )
    })
    .compile()
}
