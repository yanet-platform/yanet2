use core::error::Error;

/// Wire messages read from FIB documents and rendered in command output.
const SERDE_MESSAGES: &[&str] = &[
    ".modules.route.controlplane.routepb.v1.FIBEntry",
    ".modules.route.controlplane.routepb.v1.FIBNexthop",
];

fn main() -> Result<(), Box<dyn Error>> {
    let mut build =
        ync_build::client("../../..", &["modules/route/controlplane/routepb/v1/route.proto"]).with(|builder| {
            builder
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
                )
        });
    for message in SERDE_MESSAGES {
        build =
            build.with(|builder| builder.message_attribute(message, "#[derive(serde::Serialize, serde::Deserialize)]"));
    }

    build.compile()
}
