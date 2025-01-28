use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    tonic_build::configure().compile_protos(&["route/routepb/route.proto"], &["../../../controlplane/modules"])?;

    Ok(())
}
