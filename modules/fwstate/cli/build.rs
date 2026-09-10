use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/fwstate/controlplane/fwstatepb/v1/fwstate.proto"])
        .serialize()
        .compile()
}
