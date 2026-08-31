use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/forwardpb/v1/forward.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .extern_path(".common.filterpb.v1", "::filterpb::pb")
        .message_attribute(".", "#[derive(Serialize, Deserialize)]")
        .message_attribute(".", "#[serde(default, deny_unknown_fields)]")
        .field_attribute(
            ".modules.forward.controlplane.forwardpb.v1.Action.mode",
            "#[serde(serialize_with = \"crate::serialize_forward_mode\", deserialize_with = \"crate::deserialize_forward_mode\")]",
        )
        .field_attribute(
            ".modules.forward.controlplane.forwardpb.v1.Action.target",
            "#[serde(deserialize_with = \"crate::null_as_default\")]",
        )
        .field_attribute(
            ".modules.forward.controlplane.forwardpb.v1.Action.counter",
            "#[serde(deserialize_with = \"crate::null_as_default\")]",
        )
        .field_attribute(
            ".modules.forward.controlplane.forwardpb.v1.Rule",
            "#[serde(deserialize_with = \"crate::null_as_default\")]",
        )
        .field_attribute(
            ".modules.forward.controlplane.forwardpb.v1.UpdateConfigRequest",
            "#[serde(deserialize_with = \"crate::null_as_default\")]",
        )
        .compile_protos(&["forwardpb/v1/forward.proto"], &["../../..", "../controlplane"])?;

    Ok(())
}
