//! Generates the raw C bindings from the repository headers.

use std::{env, path::PathBuf};

fn main() {
    let manifest_dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR").expect("cargo sets CARGO_MANIFEST_DIR"));
    let repo_root = manifest_dir.join("../../..");
    let out_dir = PathBuf::from(env::var("OUT_DIR").expect("cargo sets OUT_DIR"));

    let bindings = bindgen::Builder::default()
        .header(manifest_dir.join("wrapper.h").to_string_lossy())
        .clang_arg(format!("-I{}", repo_root.display()))
        .rust_target(bindgen::RustTarget::stable(88, 0).expect("1.88 is a valid rust target"))
        .rust_edition(bindgen::RustEdition::Edition2024)
        .use_core()
        .ctypes_prefix("core::ffi")
        .parse_callbacks(Box::new(bindgen::CargoCallbacks::new()))
        // Layout structs: fields the Rust side projects into.
        .allowlist_type("device")
        .allowlist_type("device_ectx")
        .allowlist_type("packet")
        .allowlist_var("DEVICE_TYPE_LEN")
        .allowlist_function("yanet_dp_shim_.*")
        // Opaque: size and alignment only, no field Rust could form a
        // reference over. The header of a published device is mutated by C
        // under C locks; the worker and the packet front are C-owned.
        .opaque_type("cp_device")
        .opaque_type("packet_front")
        .opaque_type("dp_worker")
        .opaque_type("dp_config")
        .derive_default(false)
        .derive_debug(false)
        .layout_tests(true)
        .generate()
        .expect("bindgen failed on wrapper.h");

    bindings
        .write_to_file(out_dir.join("bindings.rs"))
        .expect("failed to write bindings.rs");
}
