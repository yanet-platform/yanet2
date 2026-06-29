use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    tonic_build::configure()
        .build_server(false)
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .field_attribute(
            ".controlplane.ynpb.v1.RegisteredBackend.last_seen_at",
            "#[serde(serialize_with = \"crate::serialize_timestamp\")]",
        )
        .field_attribute(
            ".controlplane.ynpb.v1.RegisteredBackend.kind",
            "#[serde(serialize_with = \"crate::serialize_backend_kind\")]",
        )
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .compile_protos(
            &[
                "controlplane/ynpb/v1/logging.proto",
                "controlplane/ynpb/v1/device.proto",
                "controlplane/ynpb/v1/function.proto",
                "controlplane/ynpb/v1/pipeline.proto",
                "controlplane/ynpb/v1/inspect.proto",
                "controlplane/ynpb/v1/counters.proto",
                "controlplane/ynpb/v1/gateway.proto",
            ],
            &["../../.."],
        )?;
    Ok(())
}
