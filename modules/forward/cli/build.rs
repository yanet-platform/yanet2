use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/forwardpb/forward.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".filterpb", "::filterpb::pb")
        .message_attribute(".forwardpb", "#[derive(Serialize)]")
        .field_attribute(".forwardpb.GetMetricsResponse.metrics", "#[serde(skip)]")
        .compile_protos(
            &["forwardpb/forward.proto"],
            &["../../..", "../controlplane"],
        )?;

    Ok(())
}
