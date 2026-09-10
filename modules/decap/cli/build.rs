use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/decap/controlplane/decappb/v1/decap.proto"])
        .serialize()
        .compile()
}
