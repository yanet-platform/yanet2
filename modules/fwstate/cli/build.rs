use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/fwstatepb/v1/fwstate.proto");
    println!("cargo:rerun-if-changed=../../../common/commonpb/v1/ipaddr.proto");
    println!("cargo:rerun-if-changed=../../../common/commonpb/v1/macaddr.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["fwstatepb/v1/fwstate.proto"], &["../controlplane", "../../.."])?;

    Ok(())
}
