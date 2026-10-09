//! Compiles the C fixtures and generates the C-built image the Miri tests
//! embed.

use std::{
    env,
    path::{Path, PathBuf},
    process::Command,
};

fn main() {
    let out_dir = PathBuf::from(env::var_os("OUT_DIR").expect("cargo sets OUT_DIR"));
    let repo = yanet_build::repo_root();
    println!("cargo:rerun-if-changed=csrc");
    println!("cargo:rerun-if-env-changed={}", yanet_build::BUILD_DIR_ENV);

    let mut lpm = cc::Build::new();
    lpm.file("csrc/lpm_image.c")
        .include(&repo)
        .include("csrc")
        .define("_GNU_SOURCE", None)
        .opt_level(2)
        .flag("-march=haswell")
        .flag("-Werror")
        .warnings(true)
        .extra_warnings(true);
    lpm.compile("yanet_testkit_lpm");
    generate_fixture(&lpm, &repo, &out_dir);

    if env::var_os("CARGO_FEATURE_DATAPLANE").is_some() {
        build_dataplane(&repo);
    }
}

/// Builds and runs the fixture generator natively.
///
/// Build scripts run natively even under Miri, so the image the interpreted
/// tests load is produced by the real C insert path.
fn generate_fixture(lpm: &cc::Build, repo: &Path, out_dir: &Path) {
    let exe = out_dir.join("gen_fixture");
    let compiler = lpm.get_compiler();
    let status = compiler
        .to_command()
        .args(["-O2", "-D_GNU_SOURCE", "-Icsrc"])
        .arg(format!("-I{}", repo.display()))
        .args(["csrc/gen_fixture.c", "csrc/lpm_image.c", "-o"])
        .arg(&exe)
        .status()
        .expect("run the C compiler");
    assert!(status.success(), "fixture generator failed to compile");

    let output = Command::new(&exe)
        .arg(out_dir)
        .output()
        .expect("run the fixture generator");
    assert!(output.status.success(), "fixture generator failed");
    let offset: usize = String::from_utf8(output.stdout)
        .expect("generator prints ASCII")
        .trim()
        .parse()
        .expect("generator prints the configuration offset");
    std::fs::write(
        out_dir.join("fixture.rs"),
        format!("/// Offset of the decap configuration inside the fixture image.\npub const CONFIG_OFFSET: usize = {offset};\n"),
    )
    .expect("write fixture.rs");
}

/// Compiles the packet harness with the real parser, tunnel stripper and C
/// decap handler, using the meson flags.
///
/// The C module constructor is renamed so that it cannot collide with the
/// Rust module's exported constructor in the same test binary.
fn build_dataplane(repo: &Path) {
    let meson = yanet_build::meson_flags().unwrap_or_else(|err| panic!("{err}"));
    let mut build = cc::Build::new();
    build
        .file("csrc/dataplane_harness.c")
        .file(repo.join("lib/dataplane/packet/packet.c"))
        .file(repo.join("lib/dataplane/packet/decap.c"))
        .file(repo.join("modules/decap/dataplane/dataplane.c"))
        .define("new_module_decap", Some("tk_c_new_module_decap"))
        .opt_level(2)
        .warnings(false);
    for dir in &meson.include_dirs {
        build.include(dir);
    }
    for (name, value) in &meson.defines {
        build.define(name, value.as_deref());
    }
    for flag in &meson.flags {
        build.flag(flag);
    }
    build.compile("yanet_testkit_dataplane");
}
