use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../common/proto/target.proto");
    println!("cargo:rerun-if-changed=../controlplane/dscppb/dscp.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(
            &["common/proto/target.proto", "dscppb/dscp.proto"],
            &["../../..", "../controlplane"],
        )?;

    Ok(())
}
