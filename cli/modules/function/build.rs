use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../controlplane/ynpb/function.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".commonpb", "::commonpb::pb")
        .compile_protos(&["controlplane/ynpb/function.proto"], &["../../.."])?;

    Ok(())
}
