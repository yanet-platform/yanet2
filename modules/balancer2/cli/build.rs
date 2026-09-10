use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client(
        "../../..",
        &[
            "modules/balancer2/controlplane/balancerpb/v1/balancer.proto",
            "modules/balancer2/controlplane/balancerpb/v1/config.proto",
            "modules/balancer2/controlplane/balancerpb/v1/state.proto",
            "modules/balancer2/controlplane/balancerpb/v1/filter.proto",
        ],
    )
    .serialize()
    .with(|builder| {
        builder
            .protoc_arg("--experimental_allow_proto3_optional")
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
    })
    .compile()
}
