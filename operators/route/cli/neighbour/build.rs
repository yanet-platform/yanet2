use core::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../../..", &["operators/route/operatorpb/v1/neighbour.proto"])
        .serialize()
        .with(|builder| {
            builder.field_attribute(
                ".operators.route.operatorpb.v1.NeighbourEntry.state",
                "#[serde(serialize_with = \"crate::serialize_neighbour_state\")]",
            )
        })
        .compile()
}
