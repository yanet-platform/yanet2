use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/acl/controlplane/aclpb/v1/acl.proto"])
        .with(|builder| {
            builder
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
                .message_attribute(
                    ".modules.acl.controlplane.aclpb.v1.RuleCounter",
                    "#[derive(serde::Serialize)]",
                )
                .field_attribute(
                    ".modules.acl.controlplane.aclpb.v1.Action.kind",
                    "#[serde(serialize_with = \"crate::serialize_action_kind\", deserialize_with = \"crate::deserialize_action_kind\")]",
                )
        })
        .compile()
}
