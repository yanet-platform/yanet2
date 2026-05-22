use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../operatorpb/v1/route.proto");

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
        .extern_path(".commonpb", "::commonpb::pb")
        .compile_protos(
            &["../../../../operators/route/operatorpb/v1/route.proto"],
            &["../../../../"],
        )
        .map_err(Into::into)
}
