//! Generates and compiles the ctest suite for the yanet-sys layout mirrors.
//!
//! The C side is compiled with the meson defines, include directories and
//! forced includes of the dataplane build. Setting `YANET_CTEST_DRIFT` to
//! `added`, `swapped` or `retyped` compiles against a scratch copy of the LPM
//! header with that layout drift injected, to show that ctest catches it; the
//! tracked header is never modified.

use std::{env, fs, path::PathBuf};

use ctest::TestGenerator;

/// Opaque C structs mirrored as uninhabited Rust enums.
const OPAQUE: &[&str] = &[
    "agent",
    "block_allocator",
    "config_gen_ectx",
    "counter",
    "counter_storage",
    "counter_value_handle",
    "cp_module_counter_registry",
    "cp_module_device",
    "cp_module_object",
    "device_entry_ectx",
    "dp_config",
    "dp_worker",
    "module_object_link_ectx",
    "rte_mbuf",
];

/// Monomorphic aliases of the generic wrappers and the C type each shadows.
const REL_ALIASES: &[(&str, &str)] = &[
    ("rel_lpm_chunks", "struct lpm_page **"),
    ("rel_lpm_chunk", "struct lpm_page *"),
    ("rel_lpm_value_page", "struct lpm_page *"),
    ("opaque_memory_context", "struct memory_context"),
];

const HEADERS: &[&str] = &[
    "netinet/in.h",
    "rte_ether.h",
    "common/lpm.h",
    "lib/controlplane/config/cp_module.h",
    "lib/dataplane/module/module.h",
    "lib/dataplane/module/packet_front.h",
    "lib/dataplane/packet/packet.h",
    "lib/dataplane/pipeline/econtext.h",
    "systest.h",
];

/// Layout drifts injected into a scratch copy of `common/lpm.h`.
const DRIFT_HEADER: &str = "common/lpm.h";
const DRIFT_ORIGINAL: &str = "\tstruct lpm_page **pages;\n\tsize_t page_count;\n";

fn drift_replacement(kind: &str) -> &'static str {
    match kind {
        "added" => "\tstruct lpm_page **pages;\n\tsize_t page_count;\n\tuint64_t drift_added;\n",
        "swapped" => "\tsize_t page_count;\n\tstruct lpm_page **pages;\n",
        "retyped" => "\tstruct lpm_page **pages;\n\tint64_t page_count;\n",
        other => panic!("unknown YANET_CTEST_DRIFT value {other:?}; use added, swapped or retyped"),
    }
}

fn main() {
    println!("cargo:rerun-if-env-changed=YANET_CTEST_DRIFT");
    println!("cargo:rerun-if-env-changed={}", yanet_build::BUILD_DIR_ENV);
    println!("cargo:rerun-if-changed=csrc/systest.h");
    println!("cargo:rerun-if-changed=../yanet-sys/src/ffi.rs");

    let meson = yanet_build::meson_flags().unwrap_or_else(|err| panic!("{err}"));
    let out_dir = PathBuf::from(env::var_os("OUT_DIR").expect("cargo sets OUT_DIR"));
    let manifest_dir = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").expect("cargo sets CARGO_MANIFEST_DIR"));

    let mut cfg = TestGenerator::new();
    cfg.skip_private(true);
    cfg.language(ctest::Language::C);
    cfg.edition(2024);
    // The mirrors' zerocopy derives drop out under this cfg, so the
    // standalone expansion needs no dependencies.
    cfg.cfg("ctest", None);

    if let Ok(kind) = env::var("YANET_CTEST_DRIFT") {
        let overlay = out_dir.join("drift-overlay");
        let target = overlay.join(DRIFT_HEADER);
        fs::create_dir_all(target.parent().expect("header has a parent")).expect("create overlay dir");
        let original = fs::read_to_string(meson.repo_root.join(DRIFT_HEADER)).expect("read the LPM header");
        assert!(
            original.contains(DRIFT_ORIGINAL),
            "LPM header no longer has the expected fields"
        );
        // Sibling includes are spelled relative to the header's own directory;
        // anchor them at the repository root so they keep reaching the real
        // headers from the scratch location.
        let drifted = original
            .replace(DRIFT_ORIGINAL, drift_replacement(&kind))
            .replace("#include \"", "#include \"common/");
        fs::write(&target, drifted).expect("write overlay");
        println!("cargo:warning=ctest drift demonstration: {kind} field in a scratch copy of {DRIFT_HEADER}");
        cfg.include(&overlay);
    }

    cfg.include(manifest_dir.join("csrc"));
    for dir in &meson.include_dirs {
        cfg.include(dir);
    }
    for (name, value) in &meson.defines {
        cfg.define(name, value.as_deref());
    }
    for flag in &meson.flags {
        cfg.flag(flag);
    }
    for header in HEADERS {
        cfg.header(header);
    }

    cfg.rename_type(|ty| {
        if OPAQUE.contains(&ty) {
            return Some(format!("struct {ty}"));
        }
        REL_ALIASES
            .iter()
            .find(|(alias, _)| *alias == ty)
            .map(|(_, c)| (*c).to_string())
    });
    cfg.rename_alias(|alias| {
        REL_ALIASES
            .iter()
            .find(|(name, _)| *name == alias.ident())
            .map(|(_, c)| (*c).to_string())
    });

    // The generic wrappers are checked through their monomorphic aliases.
    cfg.skip_struct(|s| s.ident() == "RelPtr" || s.ident() == "Opaque");
    // No C typedef exists for it; the systest header pins the prototype.
    cfg.skip_alias(|alias| alias.ident() == "packet_decap_fn");

    ctest::generate_test(&mut cfg, "../yanet-sys/src/ffi.rs", "ctest_ffi.rs").expect("ctest generation failed");
}
