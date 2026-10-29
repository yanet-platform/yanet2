//! Build-script support for record and constant headers of SDK API crates.

use std::{env, path::PathBuf};

use cbindgen::{Builder, Config, ExportConfig, ItemType, Language, ParseConfig, Style};

/// Generates the explicitly selected input-record and constant C header.
///
/// When YANET_SDK_HEADER_DIR is set, the output filename is the package name
/// with hyphens replaced by underscores and a .h suffix. Only listed public
/// C-layout records and public constants from explicitly selected dependencies
/// are included; C callers provide typed export prototypes. Source changes in
/// this package rerun generation, while callers must track relevant dependency
/// sources. Header generation failures fail the build.
pub fn generate_header(records: &[&str], dependencies: &[&str]) {
    println!("cargo:rerun-if-env-changed=YANET_SDK_HEADER_DIR");
    println!("cargo:rerun-if-changed=src");
    let Some(dir) = env::var_os("YANET_SDK_HEADER_DIR") else {
        return;
    };
    let manifest = env::var_os("CARGO_MANIFEST_DIR").expect("cargo sets the manifest directory");
    // A build script sees the package name only; the crate name is that name
    // with `-` written as `_`, as the exports' symbols carry it.
    let crate_name = env::var("CARGO_PKG_NAME")
        .expect("cargo sets the package name")
        .replace('-', "_");
    // Only what a constant names and what is listed reaches the header.
    let config = Config {
        usize_is_size_t: true,
        parse: ParseConfig {
            parse_deps: !dependencies.is_empty(),
            include: Some(dependencies.iter().map(|package| (*package).to_owned()).collect()),
            extra_bindings: dependencies.iter().map(|package| (*package).to_owned()).collect(),
            ..ParseConfig::default()
        },
        export: ExportConfig {
            item_types: vec![ItemType::Structs, ItemType::Constants],
            include: records.iter().map(|record| (*record).to_owned()).collect(),
            ..ExportConfig::default()
        },
        ..Config::default()
    };
    Builder::new()
        .with_config(config)
        .with_crate(PathBuf::from(manifest))
        .with_language(Language::C)
        .with_style(Style::Tag)
        .with_pragma_once(true)
        .with_no_includes()
        .with_sys_include("stddef.h")
        .with_sys_include("stdint.h")
        .generate()
        .expect("cbindgen failed")
        .write_to_file(PathBuf::from(dir).join(format!("{crate_name}.h")));
}
