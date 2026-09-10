use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/forward/controlplane/forwardpb/v1/forward.proto"])
        .with(|builder| {
            builder
                .message_attribute(".", "#[derive(serde::Serialize, serde::Deserialize)]")
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
        })
        .compile()
}
