use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=common/readinesspb/readiness.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute("readinesspb.Reason", "#[derive(serde::Serialize)]")
        .message_attribute("readinesspb.ReadyRequest", "#[derive(serde::Serialize)]")
        .message_attribute("readinesspb.ReadyResponse", "#[derive(serde::Serialize)]")
        .message_attribute("readinesspb.Scope", "#[derive(serde::Serialize)]")
        .field_attribute(
            "readinesspb.Scope.state",
            "#[serde(serialize_with = \"crate::serialize_state\")]",
        )
        .field_attribute(
            "readinesspb.Scope.observed_at",
            "#[serde(serialize_with = \"crate::serialize_timestamp\")]",
        )
        .field_attribute(
            "readinesspb.Scope.last_transition_time",
            "#[serde(serialize_with = \"crate::serialize_timestamp\")]",
        )
        .enum_attribute(".", "#[derive(serde::Serialize)]")
        .compile_protos(&["common/readinesspb/readiness.proto"], &["../../.."])
        .map_err(Into::into)
}
