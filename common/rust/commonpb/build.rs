use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=common/commonpb/target.proto");
    println!("cargo:rerun-if-changed=common/commonpb/metric.proto");

    tonic_build::configure()
        .build_server(false)
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .enum_attribute(".", "#[derive(serde::Serialize)]")
        .compile_protos(
            &["common/commonpb/target.proto", "common/commonpb/metric.proto"],
            &["../../.."],
        )?;
    Ok(())
}
