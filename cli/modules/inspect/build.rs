fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("cargo:rerun-if-changed=../../../controlplane/ynpb/inspect.proto");

    tonic_build::configure()
        .message_attribute(".", "#[derive(Serialize)]")
        .build_server(false)
        .compile_protos(&["ynpb/inspect.proto"], &["../../../controlplane/"])?;

    Ok(())
}
