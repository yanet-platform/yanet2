use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../operators/pipeline/operatorpb/v1/operator.proto");
    println!("cargo:rerun-if-changed=../../../operators/pipeline/operatorpb/v1/readiness.proto");
    println!("cargo:rerun-if-changed=../../../common/readinesspb/v1/readiness.proto");
    println!("cargo:rerun-if-changed=../../../common/commonpb/metric.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .extern_path(".common.readinesspb.v1", "::readinesspb::pb")
        .build_server(false)
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .enum_attribute(".", "#[derive(serde::Serialize)]")
        .compile_protos(
            &[
                "../../../operators/pipeline/operatorpb/v1/operator.proto",
                "../../../operators/pipeline/operatorpb/v1/readiness.proto",
            ],
            &["../../../"],
        )
        .map_err(Into::into)
}
