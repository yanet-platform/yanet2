use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/dscppb/v1/dscp.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["dscppb/v1/dscp.proto"], &["../controlplane", "../../.."])?;

    Ok(())
}
