use core::error::Error;
use std::{env, fs, path::PathBuf};

/// The dataplane header that defines the capture mode bits.
const MODE_HEADER: &str = "../dataplane/mode.h";

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client(
        "../../..",
        &[
            "modules/pdump/controlplane/pdumppb/v1/pdump.proto",
            "objects/ring/controlplane/ringpb/v1/ring.proto",
        ],
    )
    .with(|builder| builder.protoc_arg("--experimental_allow_proto3_optional"))
    .serialize()
    .compile()?;

    let out = PathBuf::from(env::var("OUT_DIR")?);
    fs::write(
        out.join("pdump_mode.rs"),
        mode_constants(&fs::read_to_string(MODE_HEADER)?)?,
    )?;
    println!("cargo:rerun-if-changed={MODE_HEADER}");

    Ok(())
}

/// Rust constants for the capture mode enumerators of the header.
///
/// Each enumerator's initialiser (`1 << 0`, `(1 << 2) - 1`) is copied as is,
/// since it is also a Rust expression; a missing enumerator fails the build.
fn mode_constants(header: &str) -> Result<String, Box<dyn Error>> {
    let mut constants = String::new();

    for name in ["PDUMP_INPUT", "PDUMP_DROPS", "PDUMP_ALL"] {
        let value = header
            .lines()
            .find_map(|line| line.trim().strip_prefix(name)?.trim_start().strip_prefix('='))
            .and_then(|rest| rest.split(',').next())
            .ok_or_else(|| format!("{MODE_HEADER} defines no {name}"))?;

        constants.push_str(&format!("pub const {name}: u32 = {};\n", value.trim()));
    }

    Ok(constants)
}
