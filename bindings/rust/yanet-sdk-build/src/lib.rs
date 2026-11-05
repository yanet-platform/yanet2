//! Build-script helper of the SDK api crates.
//!
//! An api crate's `build.rs` is
//! `fn main() { yanet_sdk_build::generate_header(&["Range", "Input"], &[]) }`.

use std::{env, path::PathBuf};

use cbindgen::{Builder, Config, ExportConfig, Language, ParseConfig, Style};

/// The packages the records of the SDK itself, such as the LPM range, come
/// from: parsed, they add to the header only the records listed.
const SDK_PACKAGES: [&str; 2] = ["yanet-sdk", "yanet-sys"];

/// The workspace directory of the SDK, where each of its packages is the
/// directory of its name, this one among them.
const SDK_DIR: &str = concat!(env!("CARGO_MANIFEST_DIR"), "/..");

/// Writes the C header of the calling crate's records and constants to
/// `$YANET_SDK_HEADER_DIR/<crate>.h`, `<crate>` being the package name with
/// `-` written as `_`, and does nothing when the variable is unset.
///
/// The header declares the `records`, the names of the crate's `pub`
/// `#[repr(C)]` structs, as `struct <Name>`, and its `pub const`s. The
/// `dependencies`, package names as spelled in their `Cargo.toml`, are parsed
/// too, and their `pub const`s reach the header: the way a module crate's
/// constants, such as its modes, get there. A record of the SDK is listed by
/// the name of a type alias in the crate: `pub type Range4 = LpmRange<4>`
/// declares `struct LpmRange_4` and `typedef struct LpmRange_4 Range4`. The
/// build script reruns on a change of `src` or of the sources of the SDK
/// packages it parses; the source of a listed dependency is for the caller's
/// script to name. A record missing from the list is missing
/// from the header, and the C side that uses it fails to compile. It does not
/// declare the exports of `#[yanet_sdk::api]`: the macro generates those, and
/// cbindgen would see them only through a nightly expansion of the crate. The C
/// side declares the prototypes over the header's types.
///
/// Panics when cbindgen fails, which fails the build.
pub fn generate_header(records: &[&str], dependencies: &[&str]) {
    println!("cargo:rerun-if-env-changed=YANET_SDK_HEADER_DIR");
    println!("cargo:rerun-if-changed=src");
    for package in SDK_PACKAGES {
        println!("cargo:rerun-if-changed={SDK_DIR}/{package}/src");
    }
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
            parse_deps: true,
            include: Some(
                SDK_PACKAGES
                    .iter()
                    .chain(dependencies)
                    .map(|package| (*package).to_owned())
                    .collect(),
            ),
            extra_bindings: dependencies.iter().map(|package| (*package).to_owned()).collect(),
            ..ParseConfig::default()
        },
        export: ExportConfig {
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
