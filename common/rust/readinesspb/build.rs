use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=common/readinesspb/v1/readiness.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute("common.readinesspb.v1.Reason", "#[derive(serde::Serialize)]")
        .message_attribute("common.readinesspb.v1.ReadyRequest", "#[derive(serde::Serialize)]")
        .message_attribute("common.readinesspb.v1.ReadyResponse", "#[derive(serde::Serialize)]")
        .message_attribute("common.readinesspb.v1.Scope", "#[derive(serde::Serialize)]")
        .field_attribute(
            "common.readinesspb.v1.Scope.state",
            "#[serde(serialize_with = \"crate::serialize_state\")]",
        )
        .field_attribute(
            "common.readinesspb.v1.Scope.observed_at",
            "#[serde(serialize_with = \"crate::serialize_timestamp\")]",
        )
        .field_attribute(
            "common.readinesspb.v1.Scope.last_transition_time",
            "#[serde(serialize_with = \"crate::serialize_timestamp\")]",
        )
        .enum_attribute(".", "#[derive(serde::Serialize)]")
        .compile_protos(&["common/readinesspb/v1/readiness.proto"], &["../../.."])
        .map_err(Into::into)
}
