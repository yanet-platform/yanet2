use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    tonic_build::configure()
        .build_server(false)
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .compile_protos(&["common/commonpb/target.proto"], &["../../.."])?;
    Ok(())
}
