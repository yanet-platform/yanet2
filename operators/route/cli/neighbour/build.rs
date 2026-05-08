use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../operatorpb/v1/neighbour.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".commonpb", "::commonpb::pb")
        .compile_protos(
            &["../../../../operators/route/operatorpb/v1/neighbour.proto"],
            &["../../../../"],
        )
        .map_err(Into::into)
}
