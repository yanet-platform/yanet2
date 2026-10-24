//! Build script of the SDK C bindings crate.
//!
//! One source of C truth: the include directories, defines and forced
//! includes of the dataplane's C sources, which meson's compilation database
//! in `YANET_BUILD_DIR` records, drive bindgen. The bindings carry bindgen's
//! own compile-time layout assertions. Generated files land in `OUT_DIR`
//! only.

#[path = "build_flags.rs"]
mod build_flags;

use std::{
    env, fs,
    path::{Path, PathBuf},
};

use build_flags::CFlags;

fn main() {
    let manifest_dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap());
    let out_dir = PathBuf::from(env::var("OUT_DIR").unwrap());
    println!("cargo:rerun-if-env-changed=LIBCLANG_PATH");
    println!("cargo:rerun-if-env-changed=CLANG_PATH");

    // The repository root is three directories above this crate.
    let root = fs::canonicalize(manifest_dir.join("../../..")).unwrap();
    let flags = CFlags::load(root);
    let shim_dir = manifest_dir.join("shim");

    generate_bindings(&flags, &shim_dir, &out_dir.join("bindings.rs"));

    println!(
        "cargo:rerun-if-changed={}",
        manifest_dir.join("build_flags.rs").display()
    );
    println!("cargo:rerun-if-changed={}", shim_dir.join("bindings.h").display());
    flags.rerun_on_headers();
}

fn generate_bindings(flags: &CFlags, shim_dir: &Path, out: &Path) {
    // The C layout is the Linux dataplane's whatever cargo targets: a
    // bare-metal check build of the dataplane crates still binds glibc
    // headers for the same architecture.
    let arch = env::var("CARGO_CFG_TARGET_ARCH").expect("target architecture");
    let bindings = bindgen::Builder::default()
        .header(shim_dir.join("bindings.h").display().to_string())
        .clang_arg(format!("--target={arch}-unknown-linux-gnu"))
        .clang_args(flags.header_args())
        .clang_arg(format!("-I{}", shim_dir.display()))
        .parse_callbacks(Box::new(bindgen::CargoCallbacks::new()))
        .rust_target(bindgen::RustTarget::stable(88, 0).expect("supported Rust target"))
        .rust_edition(bindgen::RustEdition::Edition2024)
        .use_core()
        .ctypes_prefix("core::ffi")
        .derive_debug(false)
        .derive_default(false)
        .wrap_unsafe_ops(true)
        .generate_comments(false)
        .layout_tests(true)
        .allowlist_type("lpm")
        .allowlist_type("lpm_page")
        .allowlist_type("lpm_value")
        .allowlist_var("LPM_.*")
        .generate()
        .expect("bindgen failed");
    bindings.write_to_file(out).expect("write bindings");
}
