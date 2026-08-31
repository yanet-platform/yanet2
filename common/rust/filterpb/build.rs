use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    // Decoding mirrors the Go xproto contract: an omitted or null field
    // takes its zero value and an unknown key is refused.
    let serialize_deserialize = "#[derive(serde::Serialize, serde::Deserialize)]#[serde(default, deny_unknown_fields)]";
    let null_as_default = "#[serde(deserialize_with = \"crate::null_as_default\")]";

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".common.filterpb.v1.Device", serialize_deserialize)
        .message_attribute(".common.filterpb.v1.PortRange", serialize_deserialize)
        .message_attribute(".common.filterpb.v1.ProtoRange", serialize_deserialize)
        .message_attribute(".common.filterpb.v1.VlanRange", serialize_deserialize)
        .message_attribute(".common.filterpb.v1.Fragment", serialize_deserialize)
        .field_attribute(".common.filterpb.v1.Device", null_as_default)
        .field_attribute(".common.filterpb.v1.PortRange", null_as_default)
        .field_attribute(".common.filterpb.v1.ProtoRange", null_as_default)
        .field_attribute(".common.filterpb.v1.VlanRange", null_as_default)
        .field_attribute(".common.filterpb.v1.Fragment", null_as_default)
        .compile_protos(&["common/filterpb/v1/filter.proto"], &["../../.."])?;

    Ok(())
}
