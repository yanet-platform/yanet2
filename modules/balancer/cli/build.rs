use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../common/proto/target.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/balancer.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(true)
        .build_server(false)
        .message_attribute(".", "#[derive(serde::Serialize, serde::Deserialize)]")
        .compile_protos(
            &["common/proto/target.proto", "balancerpb/balancer.proto"],
            &["../../..", "../controlplane"],
        )?;

    Ok(())
}
