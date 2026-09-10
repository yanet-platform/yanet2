use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client(
        "../../..",
        &["modules/blackhole/controlplane/blackholepb/v1/blackhole.proto"],
    )
    .serialize()
    .compile()
}
