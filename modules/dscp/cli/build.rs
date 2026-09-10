use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/dscp/controlplane/dscppb/v1/dscp.proto"])
        .serialize()
        .compile()
}
