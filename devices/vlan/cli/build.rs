use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/vlanpb/vlan.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".commonpb", "::commonpb::pb")
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["vlanpb/vlan.proto"], &["../../..", "../controlplane"])?;

    Ok(())
}
