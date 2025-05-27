use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/decappb/decap.proto");
    println!("cargo:rerun-if-changed=../../../controlplane/ynpb/inspect.proto");
    println!("cargo:rerun-if-changed=../../../common/proto/target.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(
            &["common/proto/target.proto", "decappb/decap.proto", "ynpb/inspect.proto"],
            &["../../..", "../controlplane", "../../../controlplane"],
        )?;

    Ok(())
}
