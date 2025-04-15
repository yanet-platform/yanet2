use core::error::Error;

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../../controlplane/routepb/route.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .compile_protos(&["routepb/route.proto"], &["../../controlplane"])?;

    Ok(())
}
