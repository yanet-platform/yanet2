use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/forwardpb/v1/forward.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.filterpb.v1", "::filterpb::pb")
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["forwardpb/v1/forward.proto"], &["../../..", "../controlplane"])?;

    Ok(())
}
