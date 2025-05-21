use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../common/proto/target.proto");
    println!("cargo:rerun-if-changed=../controlplane/pdumppb/pdump.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(
            &["common/proto/target.proto", "pdumppb/pdump.proto"],
            &["../../..", "../controlplane"],
        )?;

    Ok(())
}
