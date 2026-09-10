use core::error::Error;
use std::{env, path::PathBuf};

fn main() -> Result<(), Box<dyn Error>> {
    ync_build::client("../../..", &["modules/pdump/controlplane/pdumppb/v1/pdump.proto"])
        .serialize()
        .compile()?;

    let bindings = bindgen::Builder::default()
        .header("../dataplane/mode.h")
        .generate()
        .expect("Unable to generate dataplane/mode.h bindings");

    let out_path = PathBuf::from(env::var("OUT_DIR").unwrap());
    bindings
        .write_to_file(out_path.join("pdump_mode.rs"))
        .expect("Couldn't write bindings!");

    Ok(())
}
