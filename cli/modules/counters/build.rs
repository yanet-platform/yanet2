fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("cargo:rerun-if-changed=../../../controlplane/ynpb/counters.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["ynpb/counters.proto"], &["../../../controlplane/"])?;

    Ok(())
}
