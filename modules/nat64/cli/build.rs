use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/nat64pb/nat64.proto");

    tonic_build::configure()
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["nat64pb/nat64.proto"], &["../controlplane"])?;

    Ok(())
}
