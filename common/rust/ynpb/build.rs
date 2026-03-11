use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    tonic_build::configure()
        .build_server(false)
        .message_attribute(".", "#[derive(serde::Serialize)]")
        .extern_path(".commonpb", "::commonpb::pb")
        .compile_protos(
            &[
                "controlplane/ynpb/logging.proto",
                "controlplane/ynpb/function.proto",
                "controlplane/ynpb/pipeline.proto",
                "controlplane/ynpb/inspect.proto",
                "controlplane/ynpb/counters.proto",
            ],
            &["../../.."],
        )?;
    Ok(())
}
