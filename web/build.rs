use std::{io, path::PathBuf};

fn main() -> io::Result<()> {
    println!("cargo:rerun-if-changed=../modules/route/controlplane/routepb");

    // Path to the directory containing proto files.
    let route_proto_dir = PathBuf::from("../modules/route/controlplane/routepb");

    // Collect all .proto files.
    let mut proto_files = Vec::new();
    for entry in std::fs::read_dir(route_proto_dir)? {
        let entry = entry?;
        let path = entry.path();
        if path.is_file() && path.extension().unwrap_or_default() == "proto" {
            proto_files.push(path);
        }
    }

    prost_build::Config::new().compile_protos(
        &[
            "../modules/route/controlplane/routepb/route.proto",
            "../modules/route/controlplane/routepb/neighbour.proto",
        ],
        &["../modules/route/controlplane/routepb"],
    )?;

    Ok(())
}
