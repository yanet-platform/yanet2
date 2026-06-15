use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/mirrorpb/v1/mirror.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.filterpb.v1", "::filterpb::pb")
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["mirrorpb/v1/mirror.proto"], &["../../..", "../controlplane"])?;

    Ok(())
}
