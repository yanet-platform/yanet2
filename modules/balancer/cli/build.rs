use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../agent/balancerpb/balancer.proto");
    println!("cargo:rerun-if-changed=../agent/balancerpb/info.proto");
    println!("cargo:rerun-if-changed=../agent/balancerpb/module.proto");
    println!("cargo:rerun-if-changed=../agent/balancerpb/stats.proto");
    println!("cargo:rerun-if-changed=../agent/balancerpb/graph.proto");
    println!("cargo:rerun-if-changed=../../../common/commonpb/metric.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .protoc_arg("--experimental_allow_proto3_optional")
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .enum_attribute(".", "#[derive(serde::Serialize)]")
        .field_attribute("refresh_period", "#[serde(skip)]")
        .field_attribute("last_packet_timestamp", "#[serde(skip)]")
        .field_attribute("create_timestamp", "#[serde(skip)]")
        .field_attribute("timeout", "#[serde(skip)]")
        .compile_protos(
            &[
                "modules/balancer/agent/balancerpb/balancer.proto",
                "modules/balancer/agent/balancerpb/info.proto",
                "modules/balancer/agent/balancerpb/module.proto",
                "modules/balancer/agent/balancerpb/stats.proto",
                "modules/balancer/agent/balancerpb/graph.proto",
                "common/commonpb/metric.proto",
            ],
            &["../../.."],
        )?;

    Ok(())
}
