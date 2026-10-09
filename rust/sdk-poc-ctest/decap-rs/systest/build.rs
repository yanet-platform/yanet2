//! Generates the ctest suite that checks the decap configuration mirror
//! against `modules/decap/dataplane/config.h`.
//!
//! The mirror in the module crate holds only the body after the C module
//! header, while C declares one flat struct. The build parses the mirror,
//! writes a flat shadow struct (the header plus the mirror's fields, types
//! copied verbatim) for ctest to check field by field, and writes constant
//! assertions that the shadow and the SDK's header-plus-body layout agree.
//! Setting `YANET_CTEST_DRIFT` to `added`, `swapped` or `retyped` checks
//! against a scratch copy of the header with that drift injected.

use core::fmt::Write as _;
use std::{env, fs, path::PathBuf};

use ctest::TestGenerator;
use quote::ToTokens;

// The mirror: its source file relative to this crate, its type name, and its
// path from this crate.
const MIRROR_SOURCE: &str = "../src/config.rs";
const MIRROR: &str = "DecapConfig";
const MIRROR_PATH: &str = "decap_dp::DecapConfig";

// The C side: the struct the mirror's fields belong to, the name of its
// module-header field, and the header declaring it.
const C_STRUCT: &str = "decap_module_config";
const C_HEADER_FIELD: &str = "cp_module";
const C_HEADER: &str = "modules/decap/dataplane/config.h";

/// Module type name the dataplane hands these configurations to.
const MODULE_NAME: &str = "decap";

/// Field types the shadow may use: the name as written, the SDK type it
/// must resolve to, and the C type it stands for.
const TYPES: &[(&str, &str, &str)] = &[
    ("Lpm", "yanet_sys::Lpm", "struct lpm"),
    ("cp_module", "yanet_sys::ffi::cp_module", "struct cp_module"),
];

/// Field lines of the C struct the drift demonstration rewrites.
const DRIFT_ORIGINAL: &str = "\tstruct lpm prefixes4;\n\tstruct lpm prefixes6;\n";

fn drift_replacement(kind: &str) -> &'static str {
    match kind {
        "added" => "\tuint64_t drift_added;\n\tstruct lpm prefixes4;\n\tstruct lpm prefixes6;\n",
        "swapped" => "\tstruct lpm prefixes6;\n\tstruct lpm prefixes4;\n",
        "retyped" => "\tstruct lpm prefixes4;\n\tuint8_t prefixes6[sizeof(struct lpm)];\n",
        other => panic!("unknown YANET_CTEST_DRIFT value {other:?}; use added, swapped or retyped"),
    }
}

/// Named fields of the mirror struct, as (name, type tokens).
fn mirror_fields(source: &str) -> Vec<(String, String)> {
    let file = syn::parse_file(source).expect("parse the mirror source");
    let item = file
        .items
        .iter()
        .find_map(|item| match item {
            syn::Item::Struct(item) if item.ident == MIRROR => Some(item),
            _ => None,
        })
        .unwrap_or_else(|| panic!("{MIRROR_SOURCE} defines no struct {MIRROR}"));
    let repr_c = item
        .attrs
        .iter()
        .any(|attr| attr.path().is_ident("repr") && attr.meta.to_token_stream().to_string().contains("C"));
    assert!(repr_c, "{MIRROR} must be repr(C)");
    let syn::Fields::Named(fields) = &item.fields else {
        panic!("{MIRROR} must have named fields");
    };
    fields
        .named
        .iter()
        .map(|field| {
            let name = field.ident.as_ref().expect("named field").to_string();
            (name, field.ty.to_token_stream().to_string())
        })
        .collect()
}

fn main() {
    println!("cargo:rerun-if-env-changed=YANET_CTEST_DRIFT");
    println!("cargo:rerun-if-env-changed={}", yanet_build::BUILD_DIR_ENV);
    println!("cargo:rerun-if-changed={MIRROR_SOURCE}");

    let out_dir = PathBuf::from(env::var_os("OUT_DIR").expect("cargo sets OUT_DIR"));
    let fields = mirror_fields(&fs::read_to_string(MIRROR_SOURCE).expect("read the mirror source"));

    let mut shadow = format!(
        "/// Flat shadow of `struct {C_STRUCT}`, generated from `{MIRROR}`.\n#[repr(C)]\npub struct {C_STRUCT} {{\n    pub {C_HEADER_FIELD}: cp_module,\n"
    );
    for (name, ty) in &fields {
        writeln!(shadow, "    pub {name}: {ty},").unwrap();
    }
    shadow.push_str("}\n");
    // The standalone ctest expansion resolves field types, so it gets
    // placeholders; the real build uses the SDK mirrors instead.
    for (rust, _, _) in TYPES {
        writeln!(shadow, "#[cfg(ctest)]\npub enum {rust} {{}}").unwrap();
    }
    fs::write(out_dir.join("shadow.rs"), &shadow).expect("write shadow.rs");

    let mut checks = format!(
        "const _: () = assert!(size_of::<{C_STRUCT}>() == size_of::<yanet_sys::ModuleConfig<{MIRROR_PATH}>>());\n\
         const _: () = assert!(align_of::<{C_STRUCT}>() == align_of::<yanet_sys::ModuleConfig<{MIRROR_PATH}>>());\n\
         const _: () = assert!(core::mem::offset_of!({C_STRUCT}, {C_HEADER_FIELD}) == 0);\n\
         const _: () = assert!(yanet_sdk::same_name(decap_dp::YANET_MODULE_NAME, {MODULE_NAME:?}));\n\
         const _: () = yanet_sdk::same_type(\n    core::marker::PhantomData::<<decap_dp::YanetModule as yanet_sdk::Module>::Config>,\n    core::marker::PhantomData::<{MIRROR_PATH}>,\n);\n"
    );
    // The shadow copies field types as text, which resolves in this crate's
    // scope; inferring each mirror field's exact type pins it to the SDK type
    // that text must mean, so a look-alike type fails even if it derefs to it.
    checks.push_str("fn _field_type<T>(_: &T) -> core::marker::PhantomData<T> {\n    core::marker::PhantomData\n}\n");
    checks.push_str(&format!("fn _mirror_field_types(mirror: &{MIRROR_PATH}) {{\n"));
    for (name, ty) in &fields {
        let (_, sdk, _) = TYPES
            .iter()
            .find(|(rust, _, _)| rust == ty)
            .unwrap_or_else(|| panic!("field {name} of {MIRROR} has type {ty}, which has no SDK mapping"));
        writeln!(
            checks,
            "    yanet_sdk::same_type(_field_type(&mirror.{name}), core::marker::PhantomData::<{sdk}>);"
        )
        .unwrap();
    }
    checks.push_str("}\n");
    for (name, _) in &fields {
        writeln!(
            checks,
            "const _: () = assert!(core::mem::offset_of!({C_STRUCT}, {name}) == yanet_sys::body_offset::<{MIRROR_PATH}>() + core::mem::offset_of!({MIRROR_PATH}, {name}));"
        )
        .unwrap();
    }
    fs::write(out_dir.join("checks.rs"), checks).expect("write checks.rs");

    let meson = yanet_build::meson_flags().unwrap_or_else(|err| panic!("{err}"));
    let mut cfg = TestGenerator::new();
    cfg.skip_private(true)
        .language(ctest::Language::C)
        .edition(2024)
        .cfg("ctest", None);

    if let Ok(kind) = env::var("YANET_CTEST_DRIFT") {
        let overlay = out_dir.join("drift-overlay");
        let target = overlay.join(C_HEADER);
        fs::create_dir_all(target.parent().expect("header has a parent")).expect("create overlay dir");
        let original = fs::read_to_string(meson.repo_root.join(C_HEADER)).expect("read the module header");
        assert!(
            original.contains(DRIFT_ORIGINAL),
            "module header no longer has the expected fields"
        );
        fs::write(&target, original.replace(DRIFT_ORIGINAL, drift_replacement(&kind))).expect("write overlay");
        println!("cargo:warning=ctest drift demonstration: {kind} field in a scratch copy of {C_HEADER}");
        cfg.include(&overlay);
    }
    for dir in &meson.include_dirs {
        cfg.include(dir);
    }
    for (name, value) in &meson.defines {
        cfg.define(name, value.as_deref());
    }
    for flag in &meson.flags {
        cfg.flag(flag);
    }
    cfg.header(C_HEADER);
    cfg.rename_type(|ty| {
        TYPES
            .iter()
            .find(|(rust, _, _)| *rust == ty)
            .map(|(_, _, c)| (*c).to_string())
    });

    ctest::generate_test(&mut cfg, out_dir.join("shadow.rs"), "ctest_module.rs").expect("ctest generation failed");
}
