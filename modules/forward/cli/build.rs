use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../../common/filterpb/filter.proto");
    println!("cargo:rerun-if-changed=../controlplane/forwardpb/forward.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["forwardpb/forward.proto"], &["../../..", "../controlplane"])?;

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute("filterpb.Device", "#[derive(Serialize)]")
        .message_attribute("filterpb.VlanRange", "#[derive(Serialize)]")
        .message_attribute("filterpb.ProtoRange", "#[derive(Serialize)]")
        .message_attribute("filterpb.PortRange", "#[derive(Serialize)]")
        .message_attribute("filterpb.IPPrefix", "#[derive(Serialize)]")
        .compile_protos(&["common/filterpb/filter.proto"], &["../../.."])?;

    Ok(())
}
