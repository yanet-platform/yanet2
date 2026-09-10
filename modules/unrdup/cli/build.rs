use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/unrdup/controlplane/unrduppb/v1/unrdup.proto"])
        .serialize()
        .compile()
}
