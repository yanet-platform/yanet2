use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/l3bpb/v1/l3b.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.filterpb.v1", "::filterpb::pb")
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(
            &["modules/l3b/controlplane/l3bpb/v1/l3b.proto"],
            &["../../.."],
        )?;

    Ok(())
}
