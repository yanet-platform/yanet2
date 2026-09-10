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
                .message_attribute(
                    ".modules.acl.controlplane.aclpb.v1.SyncConfig",
                    "#[derive(serde::Serialize, serde::Deserialize)] #[serde(default)]",
                )
                .field_attribute(
                    ".modules.acl.controlplane.aclpb.v1.Action.kind",
                    "#[serde(serialize_with = \"crate::serialize_action_kind\", deserialize_with = \"crate::deserialize_action_kind\")]",
                )
                // The typed network lists stay out of the YAML output while empty,
                // so show rendering of a legacy-schema config is unchanged.
                .field_attribute(
                    ".modules.acl.controlplane.aclpb.v1.Rule.sources4",
                    "#[serde(skip_serializing_if = \"Vec::is_empty\")]",
                )
                .field_attribute(
                    ".modules.acl.controlplane.aclpb.v1.Rule.sources6",
                    "#[serde(skip_serializing_if = \"Vec::is_empty\")]",
                )
                .field_attribute(
                    ".modules.acl.controlplane.aclpb.v1.Rule.destinations4",
                    "#[serde(skip_serializing_if = \"Vec::is_empty\")]",
                )
                .field_attribute(
                    ".modules.acl.controlplane.aclpb.v1.Rule.destinations6",
                    "#[serde(skip_serializing_if = \"Vec::is_empty\")]",
                )
        })
        .compile()
}
