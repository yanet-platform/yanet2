use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../agents/yanet-pipeline-operator/operatorpb/operator.proto");
    println!("cargo:rerun-if-changed=../../../common/commonpb/metric.proto");

    tonic_build::configure()
        .extern_path(".commonpb", "::commonpb::pb")
        .build_server(false)
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .enum_attribute(".", "#[derive(serde::Serialize)]")
        .compile_protos(
            &["../../../agents/yanet-pipeline-operator/operatorpb/operator.proto"],
            &["../../../"],
        )
        .map_err(Into::into)
}
