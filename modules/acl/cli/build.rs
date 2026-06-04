use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/aclpb/v1/acl.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .extern_path(".filterpb", "::filterpb::pb")
        .message_attribute(
            ".modules.acl.controlplane.aclpb.v1.Rule",
            "#[derive(serde::Serialize, serde::Deserialize)] #[serde(default)]",
        )
        .message_attribute(
            ".modules.acl.controlplane.aclpb.v1.Action",
            "#[derive(serde::Serialize, serde::Deserialize)]",
        )
        .message_attribute(
            ".modules.acl.controlplane.aclpb.v1.ShowConfigResponse",
            "#[derive(serde::Serialize)]",
        )
        .field_attribute(
            ".modules.acl.controlplane.aclpb.v1.Action.kind",
            "#[serde(with = \"crate::action_kind\")]",
        )
        .field_attribute(
            ".modules.acl.controlplane.aclpb.v1.ShowConfigResponse.fwstate_name",
            "#[serde(skip)]",
        )
        .compile_protos(&["aclpb/v1/acl.proto"], &["../../..", "../controlplane"])?;

    Ok(())
}
