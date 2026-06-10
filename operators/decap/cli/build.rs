use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../operators/decap/operatorpb/v1/readiness.proto");
    println!("cargo:rerun-if-changed=../../../common/readinesspb/v1/readiness.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.readinesspb.v1", "::readinesspb::pb")
        .compile_protos(
            &["../../../operators/decap/operatorpb/v1/readiness.proto"],
            &["../../../"],
        )
        .map_err(Into::into)
}
