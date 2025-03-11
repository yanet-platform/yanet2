use std::{io, path::PathBuf};

fn main() -> io::Result<()> {
    println!("cargo:rerun-if-changed=../controlplane/modules/route/routepb");

    // Path to the directory containing proto files.
    let route_proto_dir = PathBuf::from("../controlplane/modules/route/routepb");

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
            "../controlplane/modules/route/routepb/route.proto",
            "../controlplane/modules/route/routepb/neighbour.proto",
        ],
        &["../controlplane/modules/route/routepb"],
    )?;

    Ok(())
}
