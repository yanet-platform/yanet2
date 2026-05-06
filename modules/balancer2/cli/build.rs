use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/balancer.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/config.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/state.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/filter.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .protoc_arg("--experimental_allow_proto3_optional")
        .extern_path(".filterpb", "::filterpb::pb")
        .extern_path(".commonpb", "::commonpb::pb")
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .enum_attribute(".", "#[derive(serde::Serialize)]")
        .field_attribute("balancerpb.WlcConfig.refresh_period", "#[serde(skip)]")
        .field_attribute("balancerpb.BalancerState.last_packet_timestamp", "#[serde(skip)]")
        .field_attribute("balancerpb.VsState.last_packet_timestamp", "#[serde(skip)]")
        .field_attribute("balancerpb.RealState.last_packet_timestamp", "#[serde(skip)]")
        .field_attribute("balancerpb.Session.last_packet_timestamp", "#[serde(skip)]")
        .field_attribute("balancerpb.Session.create_timestamp", "#[serde(skip)]")
        .field_attribute("balancerpb.Session.timeout", "#[serde(skip)]")
        .compile_protos(
            &[
                "modules/balancer2/controlplane/balancerpb/balancer.proto",
                "modules/balancer2/controlplane/balancerpb/config.proto",
                "modules/balancer2/controlplane/balancerpb/state.proto",
                "modules/balancer2/controlplane/balancerpb/filter.proto",
            ],
            &["../../.."],
        )?;

    Ok(())
}
