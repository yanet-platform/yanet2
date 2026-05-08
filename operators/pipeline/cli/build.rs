use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../operators/pipeline/operatorpb/v1/operator.proto");
    println!("cargo:rerun-if-changed=../../../common/commonpb/metric.proto");

    tonic_build::configure()
        .extern_path(".commonpb", "::commonpb::pb")
        .build_server(false)
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .enum_attribute(".", "#[derive(serde::Serialize)]")
        .compile_protos(
            &["../../../operators/pipeline/operatorpb/v1/operator.proto"],
            &["../../../"],
        )
        .map_err(Into::into)
}
