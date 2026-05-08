use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../operatorpb/v1/route.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .compile_protos(
            &["../../../../operators/route/operatorpb/v1/route.proto"],
            &["../../../../"],
        )
        .map_err(Into::into)
}
