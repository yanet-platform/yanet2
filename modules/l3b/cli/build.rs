use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/l3b/controlplane/l3bpb/v1/l3b.proto"])
        .with(|builder| builder.message_attribute(".", "#[derive(Serialize)]"))
        .compile()
}
