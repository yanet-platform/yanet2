use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    // The repo-root include dir lets the proto import shared common protos;
    // a template copy points it at its own yanet2 checkout.
    ync_build::client("../../..", &["sdk/example/controlplane/examplepb/v1/example.proto"])
        .serialize()
        .compile()
}
