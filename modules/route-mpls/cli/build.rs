use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../common/commonpb/v1/ipprefix.proto");
    println!("cargo:rerun-if-changed=../../../common/commonpb/v1/ipaddr.proto");
    println!("cargo:rerun-if-changed=../controlplane/routemplspb/v1/routempls.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .message_attribute(".", "#[derive(Serialize)]")
        .enum_attribute(".", "#[derive(serde::Serialize)]")
        .compile_protos(&["routemplspb/v1/routempls.proto"], &["../../..", "../controlplane"])?;

    Ok(())
}
