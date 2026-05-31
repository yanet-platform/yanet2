use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/aclpb/v1/acl.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .extern_path(".commonpb", "::commonpb::pb")
        .extern_path(".filterpb", "::filterpb::pb")
        .compile_protos(&["aclpb/v1/acl.proto"], &["../../..", "../controlplane"])?;

    Ok(())
}
