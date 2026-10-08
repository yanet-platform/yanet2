use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["devices/vxlan/controlplane/vxlanpb/v1/vxlan.proto"])
        .serialize()
        .compile()
}
