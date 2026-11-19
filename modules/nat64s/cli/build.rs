use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/nat64s/controlplane/nat64spb/v1/nat64s.proto"])
        .with(|builder| {
            builder
                .message_attribute(".", "#[derive(serde::Serialize, serde::Deserialize)]")
                .message_attribute(".", "#[serde(default, deny_unknown_fields)]")
        })
        .compile()
}
