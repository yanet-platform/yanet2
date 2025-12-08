use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../common/commonpb/target.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/balancer.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(true)
        .build_server(false)
        .compile_protos(
            &["common/commonpb/target.proto", "balancerpb/balancer.proto"],
            &["../../..", "../controlplane"],
        )?;

    Ok(())
}
