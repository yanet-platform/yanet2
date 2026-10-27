//! Build script of the SDK C bindings crate.
//!
//! One source of C truth: the compiler, include directories, defines,
//! warning options and forced includes of the dataplane's C sources, which
//! meson's compilation database in `YANET_BUILD_DIR` records, drive bindgen
//! and the dataplane C shim. The bindings carry bindgen's own compile-time
//! layout assertions. Generated files land in `OUT_DIR` only.

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
    build_shim(&flags, &shim_dir);

    println!(
        "cargo:rerun-if-changed={}",
        manifest_dir.join("build_flags.rs").display()
    );
    println!("cargo:rerun-if-changed={}", shim_dir.join("bindings.h").display());
    flags.rerun_on_headers();
}

/// C sources of `lib/dataplane/packet` the shim's encapsulation calls.
const PACKET_SOURCES: [&str; 3] = [
    "lib/dataplane/packet/encap.c",
    "lib/dataplane/packet/decap.c",
    "lib/dataplane/packet/packet.c",
];

/// Compiles the dataplane shim with the flags of the dataplane's C sources.
///
/// The shim wraps header-inline C routing helpers and the packet
/// encapsulation routines, whose sources it compiles in. It is linked into
/// every module plugin with hidden visibility, so a plugin exports nothing of
/// it and imports none of the encapsulation from the host.
fn build_shim(flags: &CFlags, shim_dir: &Path) {
    let mut build = cc::Build::new();
    build
        .compiler(&flags.compiler)
        .file(shim_dir.join("dp_shim.c"))
        .files(PACKET_SOURCES.map(|source| flags.source(source)))
        .include(shim_dir)
        .opt_level(2)
        // Not silencing: drops cc-rs's own warning set, so the build's options alone apply.
        .warnings(false)
        .flag("-fvisibility=hidden");
    for arg in flags.header_args().iter().chain(flags.warning_args()) {
        build.flag(arg);
    }
    println!("cargo:rerun-if-changed={}", shim_dir.join("dp_shim.c").display());
    println!("cargo:rerun-if-changed={}", shim_dir.join("dp_shim.h").display());
    build.compile("yanet_sys_dp_shim");
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
        // bindgen omits a hidden-visibility function; the attribute is for
        // the C compiler that builds the shim, not for the Rust binding.
        .clang_arg("-DYANET_SYS_BINDGEN")
        .parse_callbacks(Box::new(bindgen::CargoCallbacks::new()))
        .rust_target(bindgen::RustTarget::stable(88, 0).expect("supported Rust target"))
        .rust_edition(bindgen::RustEdition::Edition2024)
        .use_core()
        .ctypes_prefix("core::ffi")
        .derive_debug(false)
        // Default zero-fills a struct where it cannot be derived, so C enums
        // stay integer constants.
        //
        // Zero is a valid value of every field bindgen makes from C (integers,
        // bools, bitfield units, raw pointers, arrays, unions, Option function
        // pointers); a Rust enum without a zero variant would make it UB.
        .default_enum_style(bindgen::EnumVariation::Consts)
        .derive_default(true)
        .wrap_unsafe_ops(true)
        .generate_comments(false)
        .layout_tests(true)
        .allowlist_type("lpm")
        .allowlist_type("lpm_page")
        .allowlist_type("lpm_value")
        .allowlist_type("counter_storage")
        .allowlist_type("cp_module")
        .allowlist_type("module")
        .allowlist_type("module_ectx")
        .allowlist_type("module_device_target")
        .allowlist_type("dp_worker")
        .allowlist_type("packet")
        .allowlist_type("packet_list")
        .allowlist_type("packet_front")
        .allowlist_type("rte_mbuf")
        .allowlist_function("yanet_sys_dp_.*")
        .allowlist_var("LPM_.*")
        .allowlist_var("YANET_MODULE_ABI_VERSION")
        .allowlist_var("MODULE_TYPE_LEN")
        .allowlist_var("MBUF_MAX_SIZE")
        .allowlist_var("RTE_PKTMBUF_HEADROOM")
        .allowlist_var("PACKET_TRANSPORT_HEADER_UNAVAILABLE")
        // Reached only behind pointers the SDK never follows, their own bytes
        // never read.
        .opaque_type("rte_mempool")
        .opaque_type("rte_mbuf_ext_shared_info")
        .opaque_type("dp_config")
        .opaque_type("cp_config")
        .opaque_type("agent")
        .opaque_type("cp_config_gen")
        .opaque_type("config_gen_ectx")
        .opaque_type("device_entry_ectx")
        .opaque_type("module_object_link_ectx")
        .generate()
        .expect("bindgen failed");
    bindings.write_to_file(out).expect("write bindings");
}
