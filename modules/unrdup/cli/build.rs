use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/unrduppb/v1/unrdup.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(Serialize)]")
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .extern_path(".common.filterpb.v1", "::filterpb::pb")
        .compile_protos(&["unrduppb/v1/unrdup.proto"], &["../controlplane", "../../.."])?;

    Ok(())
}
