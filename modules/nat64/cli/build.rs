use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/nat64/controlplane/nat64pb/v1/nat64.proto"])
        .serialize()
        .compile()
}
