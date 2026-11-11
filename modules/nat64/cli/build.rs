use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/nat64/controlplane/nat64pb/v1/nat64.proto"])
        .with(|builder| {
            builder
                .message_attribute(".", "#[derive(serde::Serialize, serde::Deserialize)]")
                .message_attribute(".", "#[serde(default, deny_unknown_fields)]")
        })
        .compile()
}
