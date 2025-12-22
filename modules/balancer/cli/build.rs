use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/balancer.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/info.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/module.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/stats.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .compile_protos(
            &[
                "modules/balancer/controlplane/balancerpb/balancer.proto",
                "modules/balancer/controlplane/balancerpb/info.proto",
                "modules/balancer/controlplane/balancerpb/module.proto",
                "modules/balancer/controlplane/balancerpb/stats.proto",
            ],
            &["../../.."],
        )?;

    Ok(())
}
