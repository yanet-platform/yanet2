use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../controlplane/ynpb/pipeline.proto");

    tonic_build::configure().compile_protos(&["ynpb/pipeline.proto"], &["../../../controlplane"])?;

    Ok(())
}
