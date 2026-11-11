use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["objects/ring/controlplane/ringpb/v1/ring.proto"])
        .serialize()
        .compile()
}
