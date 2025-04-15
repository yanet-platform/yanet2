use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/forwardpb/forward.proto");

    tonic_build::configure()
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["forwardpb/forward.proto"], &["../controlplane"])?;

    Ok(())
}
