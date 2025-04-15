fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("cargo:rerun-if-changed=../../controlplane/routepb/neighbour.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .compile_protos(
            &["../../controlplane/routepb/neighbour.proto"],
            &["../../controlplane/routepb"],
        )?;

    Ok(())
}
