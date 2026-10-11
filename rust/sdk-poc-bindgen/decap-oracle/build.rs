//! Compiles the C decap module and packet helpers with exactly the flags
//! yanet-sys exports as `DEP_YANET_*` metadata.

use std::{env, path::PathBuf};

fn metadata(key: &str) -> Vec<String> {
    let value = env::var(format!("DEP_YANET_{key}")).unwrap_or_else(|_| panic!("yanet-sys did not export {key}"));
    value.split(';').filter(|s| !s.is_empty()).map(str::to_owned).collect()
}

fn main() {
    let root = PathBuf::from(env::var("DEP_YANET_ROOT").expect("yanet-sys did not export ROOT"));
    let compiler = env::var("DEP_YANET_COMPILER").expect("yanet-sys did not export COMPILER");
    let mut build = cc::Build::new();
    build
        .compiler(compiler)
        .file("src/oracle.c")
        .include(&root)
        .opt_level(2)
        .warnings(false)
        // The Rust module exports the real constructor name.
        .define("new_module_decap", "oracle_new_module_decap");
    for include in metadata("INCLUDE") {
        build.include(include);
    }
    for define in metadata("DEFINES") {
        let define = define.trim_start_matches("-D").to_owned();
        match define.split_once('=') {
            Some((name, value)) => build.define(name, value),
            None => build.define(&define, None),
        };
    }
    for forced in metadata("FORCED_INCLUDES") {
        build.flag("-include").flag(forced);
    }
    for machine in metadata("MACHINE") {
        build.flag(machine);
    }
    println!("cargo:rerun-if-changed=src/oracle.c");
    println!(
        "cargo:rerun-if-changed={}",
        root.join("modules/decap/dataplane/dataplane.c").display()
    );
    build.compile("decap_oracle");
}
