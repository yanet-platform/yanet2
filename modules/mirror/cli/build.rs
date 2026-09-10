use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/mirror/controlplane/mirrorpb/v1/mirror.proto"])
        .serialize()
        .compile()
}
