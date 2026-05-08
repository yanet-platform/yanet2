use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../operatorpb/neighbour.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".commonpb", "::commonpb::pb")
        .compile_protos(
            &["../../../../operators/yanet-route-operator/operatorpb/neighbour.proto"],
            &["../../../../"],
        )
        .map_err(Into::into)
}
