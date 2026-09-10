use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../../..", &["operators/route/operatorpb/v1/route.proto"])
        .serialize()
        .with(|builder| {
            builder.field_attribute(
                ".operators.route.operatorpb.v1.Route.source",
                "#[serde(serialize_with = \"crate::serialize_route_source\")]",
            )
        })
        .compile()
}
