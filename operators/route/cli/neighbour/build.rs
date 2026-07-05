use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../operatorpb/v1/neighbour.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .compile_protos(
            &["../../../../operators/route/operatorpb/v1/neighbour.proto"],
            &["../../../../"],
        )
        .map_err(Into::into)
}
