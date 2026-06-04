use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../operatorpb/v1/route.proto");
    println!("cargo:rerun-if-changed=../../operatorpb/v1/readiness.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .field_attribute(
            ".operators.route.operatorpb.v1.Route.source",
            "#[serde(serialize_with = \"crate::serialize_route_source\")]",
        )
        .field_attribute(
            ".operators.route.operatorpb.v1.Route.next_hop",
            "#[serde(serialize_with = \"crate::serialize_ip_addr\")]",
        )
        .field_attribute(
            ".operators.route.operatorpb.v1.Route.peer",
            "#[serde(serialize_with = \"crate::serialize_ip_addr\")]",
        )
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .extern_path(".readinesspb", "::readinesspb::pb")
        .compile_protos(
            &[
                "../../../../operators/route/operatorpb/v1/route.proto",
                "../../../../operators/route/operatorpb/v1/readiness.proto",
            ],
            &["../../../../"],
        )
        .map_err(Into::into)
}
