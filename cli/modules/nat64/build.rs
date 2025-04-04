use core::error::Error;
pub fn main() -> Result<(), Box<dyn Error>> {
    tonic_build::configure()
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["nat64/nat64pb/nat64.proto"], &["../../../controlplane/modules"])?;

    Ok(())
}
