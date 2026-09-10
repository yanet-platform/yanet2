use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["devices/trafgen/controlplane/trafgenpb/v1/trafgen.proto"])
        .serialize()
        .compile()
}
