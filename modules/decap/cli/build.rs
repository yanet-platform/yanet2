use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/decappb/decap.proto");

    tonic_build::configure()
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["decappb/decap.proto"], &["../controlplane"])?;

    Ok(())
}
