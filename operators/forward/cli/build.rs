use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../operators/forward/operatorpb/v1/readiness.proto");
    println!("cargo:rerun-if-changed=../../../common/readinesspb/readiness.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".readinesspb", "::readinesspb::pb")
        .compile_protos(
            &["../../../operators/forward/operatorpb/v1/readiness.proto"],
            &["../../../"],
        )
        .map_err(Into::into)
}
