use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/plainpb/v1/plain.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".commonpb", "::commonpb::pb")
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["plainpb/v1/plain.proto"], &["../../..", "../controlplane"])?;

    Ok(())
}
