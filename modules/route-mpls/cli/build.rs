use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client(
        "../../..",
        &["modules/route-mpls/controlplane/routemplspb/v1/routempls.proto"],
    )
    .serialize()
    .with(|builder| builder.enum_attribute(".", "#[derive(serde::Serialize)]"))
    .compile()
}
