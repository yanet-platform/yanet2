use core::error::Error;
use std::{env, path::PathBuf};

pub fn main() -> Result<(), Box<dyn Error>> {
    println!("cargo:rerun-if-changed=../controlplane/pdumppb/pdump.proto");

    tonic_build::configure()
        .emit_rerun_if_changed(false)
        .build_server(false)
        .message_attribute(".", "#[derive(Serialize)]")
        .compile_protos(&["pdumppb/pdump.proto"], &["../controlplane"])?;

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
