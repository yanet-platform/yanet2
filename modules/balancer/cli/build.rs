use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/balancer.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/common.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/filter.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/state.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/memory.proto");
    println!("cargo:rerun-if-changed=../../../common/filterpb/filter.proto");
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
                "modules/balancer/controlplane/balancerpb/balancer.proto",
                "common/filterpb/filter.proto",
                "common/commonpb/metric.proto",
            ],
            &["../../.."],
        )?;

    Ok(())
}
