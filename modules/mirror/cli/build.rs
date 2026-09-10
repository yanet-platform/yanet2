use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/mirror/controlplane/mirrorpb/v1/mirror.proto"])
        .with(|builder| {
            builder
                .message_attribute(".", "#[derive(serde::Serialize, serde::Deserialize)]")
                .message_attribute(".", "#[serde(default, deny_unknown_fields)]")
                .field_attribute(
                    ".modules.mirror.controlplane.mirrorpb.v1.Action.mode",
                    "#[serde(serialize_with = \"crate::serialize_mirror_mode\", deserialize_with = \"crate::deserialize_mirror_mode\")]",
                )
                .field_attribute(
                    ".modules.mirror.controlplane.mirrorpb.v1.Action.target",
                    "#[serde(deserialize_with = \"filterpb::null_as_default\")]",
                )
                .field_attribute(
                    ".modules.mirror.controlplane.mirrorpb.v1.Action.counter",
                    "#[serde(deserialize_with = \"filterpb::null_as_default\")]",
                )
                .field_attribute(
                    ".modules.mirror.controlplane.mirrorpb.v1.Rule",
                    "#[serde(deserialize_with = \"filterpb::null_as_default\")]",
                )
                .field_attribute(
                    ".modules.mirror.controlplane.mirrorpb.v1.UpdateConfigRequest",
                    "#[serde(deserialize_with = \"filterpb::null_as_default\")]",
                )
        })
        .compile()
}
