use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/aclpb/v1/acl.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".filterpb", "::filterpb::pb")
        .extern_path(".commonpb", "::commonpb::pb")
        .compile_protos(&["acl.proto"], &["../../..", "../controlplane/aclpb/v1"])?;

    Ok(())
}
