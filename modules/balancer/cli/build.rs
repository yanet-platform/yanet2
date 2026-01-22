use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../agent/balancerpb/balancer.proto");
    println!("cargo:rerun-if-changed=../agent/balancerpb/info.proto");
    println!("cargo:rerun-if-changed=../agent/balancerpb/module.proto");
    println!("cargo:rerun-if-changed=../agent/balancerpb/stats.proto");
    println!("cargo:rerun-if-changed=../agent/balancerpb/graph.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .protoc_arg("--experimental_allow_proto3_optional")
        .compile_protos(
            &[
                "modules/balancer/agent/balancerpb/balancer.proto",
                "modules/balancer/agent/balancerpb/info.proto",
                "modules/balancer/agent/balancerpb/module.proto",
                "modules/balancer/agent/balancerpb/stats.proto",
                "modules/balancer/agent/balancerpb/graph.proto",
            ],
            &["../../.."],
        )?;

    Ok(())
}
