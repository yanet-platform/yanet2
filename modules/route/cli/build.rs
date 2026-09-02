use core::error::Error;

/// Messages that gain the ordinary `Serialize`/`Deserialize` derive, plus
/// `#[serde(deny_unknown_fields)]`.
///
/// `FibShow`'s `--format json` and `fib update`'s YAML loader both go
/// straight through `FIBEntry`/`FIBNexthop` -- see `src/main.rs` -- so
/// those are the only local messages that need it. The `IPRange` and
/// `MACAddress` fields they carry are `extern_path`'d to `commonpb::pb`
/// below and already have their own hand-written serde impls there.
/// `deny_unknown_fields` only shapes `Deserialize`, turning a stray or
/// retired key inside a YAML entry into a load error instead of silently
/// discarding it -- see `FibConfig`'s doc in `src/main.rs`.
const SERDE_MESSAGES: &[&str] = &[
    ".modules.route.controlplane.routepb.v1.FIBEntry",
    ".modules.route.controlplane.routepb.v1.FIBNexthop",
];

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/routepb/v1/route.proto");

    let mut config = tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        // Lets a YAML entry that carries no nexthops omit `nexthops`
        // entirely instead of spelling out `nexthops: []` -- see
        // `UpdateFIBRequest`'s proto doc comment: such an entry is skipped
        // entirely rather than clearing anything, but that omission is
        // still a legitimate, deliberate shape to write.
        .field_attribute(
            ".modules.route.controlplane.routepb.v1.FIBEntry.nexthops",
            "#[serde(default)]",
        )
        // `counter` is a plain `String`, so prost's derived `Deserialize`
        // has no `Option` to default on its own. Empty is the normal
        // uncounted state, so `skip_serializing_if` drops the key on write
        // and `default` restores it on read, instead of a dead
        // `"counter":""` on every uncounted row.
        .field_attribute(
            ".modules.route.controlplane.routepb.v1.FIBNexthop.counter",
            "#[serde(default, skip_serializing_if = \"String::is_empty\")]",
        );
    for message in SERDE_MESSAGES {
        config = config
            .message_attribute(message, "#[derive(serde::Serialize, serde::Deserialize)]")
            .message_attribute(message, "#[serde(deny_unknown_fields)]");
    }
    config.compile_protos(&["modules/route/controlplane/routepb/v1/route.proto"], &["../../../"])?;

    Ok(())
}
