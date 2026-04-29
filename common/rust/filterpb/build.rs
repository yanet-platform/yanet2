use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    // `IpNet` is intentionally excluded from the blanket `Serialize` derive
    // because its prost-generated form (raw `addr`/`mask` byte vectors) is not
    // a useful JSON representation. A manual `Serialize` impl that emits
    // `"a.b.c.d/len"` strings lives in `src/network.rs`.
    let serialize = "#[derive(serde::Serialize)]";

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".filterpb.Device", serialize)
        .message_attribute(".filterpb.VlanRange", serialize)
        .message_attribute(".filterpb.IPPrefix", serialize)
        .message_attribute(".filterpb.ProtoRange", serialize)
        .message_attribute(".filterpb.PortRange", serialize)
        .compile_protos(&["common/filterpb/filter.proto"], &["../../.."])?;

    Ok(())
}
