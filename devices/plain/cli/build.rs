use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["devices/plain/controlplane/plainpb/v1/plain.proto"])
        .serialize()
        .compile()
}
