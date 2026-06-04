use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    let serialize = "#[derive(serde::Serialize)]";
    let serialize_deserialize = "#[derive(serde::Serialize, serde::Deserialize)]";

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".common.filterpb.v1.IPPrefix", serialize)
        .message_attribute(".common.filterpb.v1.Device", serialize_deserialize)
        .message_attribute(".common.filterpb.v1.PortRange", serialize_deserialize)
        .message_attribute(".common.filterpb.v1.ProtoRange", serialize_deserialize)
        .message_attribute(".common.filterpb.v1.VlanRange", serialize_deserialize)
        .message_attribute(".common.filterpb.v1.Fragment", serialize_deserialize)
        .compile_protos(&["common/filterpb/v1/filter.proto"], &["../../.."])?;

    Ok(())
}
