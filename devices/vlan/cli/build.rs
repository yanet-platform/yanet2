use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["devices/vlan/controlplane/vlanpb/v1/vlan.proto"])
        .serialize()
        .compile()
}
