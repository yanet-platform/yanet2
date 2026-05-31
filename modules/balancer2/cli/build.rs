use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/v1/balancer.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/v1/config.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/v1/state.proto");
    println!("cargo:rerun-if-changed=../controlplane/balancerpb/v1/filter.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .protoc_arg("--experimental_allow_proto3_optional")
        .extern_path(".filterpb", "::filterpb::pb")
        .extern_path(".commonpb", "::commonpb::pb")
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .enum_attribute(".", "#[derive(serde::Serialize)]")
        .field_attribute(
            "modules.balancer2.controlplane.balancerpb.v1.WlcConfig.refresh_period",
            "#[serde(skip)]",
        )
        .field_attribute(
            "modules.balancer2.controlplane.balancerpb.v1.BalancerState.last_packet_timestamp",
            "#[serde(skip)]",
        )
        .field_attribute(
            "modules.balancer2.controlplane.balancerpb.v1.VsState.last_packet_timestamp",
            "#[serde(skip)]",
        )
        .field_attribute(
            "modules.balancer2.controlplane.balancerpb.v1.RealState.last_packet_timestamp",
            "#[serde(skip)]",
        )
        .field_attribute(
            "modules.balancer2.controlplane.balancerpb.v1.Session.last_packet_timestamp",
            "#[serde(skip)]",
        )
        .field_attribute(
            "modules.balancer2.controlplane.balancerpb.v1.Session.create_timestamp",
            "#[serde(skip)]",
        )
        .field_attribute(
            "modules.balancer2.controlplane.balancerpb.v1.Session.timeout",
            "#[serde(skip)]",
        )
        .compile_protos(
            &[
                "modules/balancer2/controlplane/balancerpb/v1/balancer.proto",
                "modules/balancer2/controlplane/balancerpb/v1/config.proto",
                "modules/balancer2/controlplane/balancerpb/v1/state.proto",
                "modules/balancer2/controlplane/balancerpb/v1/filter.proto",
            ],
            &["../../.."],
        )?;

    Ok(())
}
