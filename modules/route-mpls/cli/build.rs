use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../common/filterpb/filter.proto");
    println!("cargo:rerun-if-changed=../controlplane/route-mplspb/route-mpls.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(Serialize)]")
        .enum_attribute(".", "#[derive(serde::Serialize)]")
        .compile_protos(&["route-mpls.proto"], &["../../..", "../controlplane/route-mplspb"])?;

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["common/filterpb/filter.proto"], &["../../.."])?;

    Ok(())
}
