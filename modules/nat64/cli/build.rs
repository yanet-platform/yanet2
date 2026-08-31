use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/nat64pb/v1/nat64.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".common.commonpb.v1", "::commonpb::pb")
        .message_attribute(".", "#[derive(Serialize)]")
        .field_attribute(
            ".modules.nat64.controlplane.nat64pb.v1.Prefix.prefix",
            "#[serde(serialize_with = \"crate::serialize_prefix\")]",
        )
        .compile_protos(&["nat64pb/v1/nat64.proto"], &["../controlplane", "../../.."])?;

    Ok(())
}
