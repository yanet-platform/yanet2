use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    let serialize = "#[derive(serde::Serialize)]";
    let serialize_deserialize = "#[derive(serde::Serialize, serde::Deserialize)]";

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".filterpb.IPPrefix", serialize)
        .message_attribute(".filterpb.Device", serialize_deserialize)
        .message_attribute(".filterpb.PortRange", serialize_deserialize)
        .message_attribute(".filterpb.ProtoRange", serialize_deserialize)
        .message_attribute(".filterpb.VlanRange", serialize_deserialize)
        .compile_protos(&["common/filterpb/filter.proto"], &["../../.."])?;

    Ok(())
}
