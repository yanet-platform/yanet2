use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client(
        "../../..",
        &["objects/fwstate/controlplane/fwstatemappb/v1/fwstatemap.proto"],
    )
    .serialize()
    .compile()
}
