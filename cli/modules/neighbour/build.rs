fn main() -> Result<(), Box<dyn std::error::Error>> {
    println!("cargo:rerun-if-changed=../../../controlplane/modules/route/routepb/neighbour.proto");

    tonic_build::configure()
        .build_server(false)
        .compile_protos(
            &["../../../controlplane/modules/route/routepb/neighbour.proto"],
            &["../../../controlplane/modules/route/routepb"],
        )?;

    Ok(())
}
