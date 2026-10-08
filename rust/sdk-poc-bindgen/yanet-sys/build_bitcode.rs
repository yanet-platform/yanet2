//! Toolchain checks of the `bitcode` build, where the C helpers are compiled
//! to LLVM bitcode and inlined into Rust at link time.
//!
//! Cross-language inlining needs one LLVM on both sides: rustc emits bitcode
//! for its own LLVM, clang for its own, and lld merges both. A mismatched
//! pair either fails at link time or silently leaves every helper as an
//! out-of-line call, so every mismatch fails the build here instead.

use std::{
    env, fs,
    path::{Path, PathBuf},
    process::Command,
};

/// Tools that produce and archive the C bitcode.
pub struct Toolchain {
    pub clang: PathBuf,
    pub archiver: PathBuf,
}

/// Major version that follows the first occurrence of a marker, as in
/// `clang version 20.1.2`.
fn major_after(text: &str, marker: &str) -> Option<u32> {
    let rest = &text[text.find(marker)? + marker.len()..];
    rest.trim_start().split('.').next()?.parse().ok()
}

fn tool_output(tool: &Path, args: &[&str]) -> String {
    let output = Command::new(tool)
        .args(args)
        .output()
        .unwrap_or_else(|err| panic!("bitcode build: cannot run {}: {err}", tool.display()));
    assert!(
        output.status.success(),
        "bitcode build: `{} {}` failed:\n{}",
        tool.display(),
        args.join(" "),
        String::from_utf8_lossy(&output.stderr)
    );
    String::from_utf8_lossy(&output.stdout).into_owned()
}

/// Codegen flags as `-C` options, with `-C x` and `-Cx` folded together.
fn codegen_flags() -> Vec<String> {
    let encoded = env::var("CARGO_ENCODED_RUSTFLAGS").unwrap_or_default();
    let mut flags = Vec::new();
    let mut tokens = encoded.split('\x1f').filter(|t| !t.is_empty());
    while let Some(token) = tokens.next() {
        match token {
            "-C" | "--codegen" => flags.extend(tokens.next().map(str::to_owned)),
            _ => flags.extend(token.strip_prefix("-C").map(str::to_owned)),
        }
    }
    flags
}

/// Checks the bitcode toolchain and returns the C compiler and archiver.
///
/// The Rust code must be generated for the CPU of the meson C module's
/// `-march`, since LLVM refuses to inline a function built for more CPU
/// features than its caller has.
pub fn check(march: Option<&str>, out_dir: &Path) -> Toolchain {
    for var in ["RUSTC", "RUSTC_LINKER", "YANET_BITCODE_CLANG", "YANET_BITCODE_AR"] {
        println!("cargo:rerun-if-env-changed={var}");
    }
    let rustc = PathBuf::from(env::var("RUSTC").expect("cargo sets RUSTC"));
    let rustc_version = tool_output(&rustc, &["-vV"]);
    let llvm = major_after(&rustc_version, "LLVM version:")
        .unwrap_or_else(|| panic!("bitcode build: no LLVM version in `rustc -vV`:\n{rustc_version}"));

    let codegen = codegen_flags();
    // rustc applies the last of repeated codegen options.
    let last = |name: &str| {
        codegen.iter().rev().find_map(|f| {
            f.strip_prefix(name)
                .and_then(|v| v.strip_prefix('='))
                .or((f == name).then_some(""))
        })
    };
    // A value other than a negative one is a switch or a plugin path.
    assert!(
        last("linker-plugin-lto").is_some_and(|v| !matches!(v, "no" | "n" | "off" | "false")),
        "bitcode build: the `bitcode` feature needs `-Clinker-plugin-lto` in the target rustflags; build \
         through `--config .cargo/bitcode.toml` (see README)"
    );
    if let Some(march) = march {
        assert!(
            last("target-cpu") == Some(march),
            "bitcode build: the meson C module uses -march={march}; the Rust code needs \
             `-Ctarget-cpu={march}` or no C helper is inlined into it"
        );
    }

    let clang = env::var_os("YANET_BITCODE_CLANG")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from(format!("clang-{llvm}")));
    if Command::new(&clang).arg("--version").output().is_err() {
        panic!(
            "bitcode build: rustc uses LLVM {llvm} and no {} is installed; cross-language inlining needs clang \
             with the same LLVM major (rustc 1.88 is LLVM 20, for clang-20), or set YANET_BITCODE_CLANG",
            clang.display()
        );
    }
    let clang_version = tool_output(&clang, &["--version"]);
    let clang_llvm = major_after(&clang_version, "clang version").unwrap_or_else(|| {
        panic!(
            "bitcode build: {} does not report a clang version:\n{clang_version}",
            clang.display()
        )
    });
    assert_eq!(
        llvm,
        clang_llvm,
        "bitcode build: rustc uses LLVM {llvm} but {} is LLVM {clang_llvm}; cross-language inlining needs the same \
         LLVM major on both sides (pin the rustc toolchain or set YANET_BITCODE_CLANG)",
        clang.display()
    );

    // The linker runs the LTO step, so it reads both bitcode streams. A
    // linker in the rustflags comes after the configured one and wins.
    let linker = last("linker")
        .map(PathBuf::from)
        .or_else(|| env::var_os("RUSTC_LINKER").map(PathBuf::from))
        .unwrap_or_else(|| panic!("bitcode build: no target linker configured; it must be clang-{llvm} driving lld"));
    let linker_llvm = major_after(&tool_output(&linker, &["--version"]), "clang version");
    assert_eq!(
        Some(llvm),
        linker_llvm,
        "bitcode build: the linker {} must be clang with LLVM {llvm}",
        linker.display()
    );
    let link_args: Vec<&str> = codegen
        .iter()
        .flat_map(|f| match (f.strip_prefix("link-arg="), f.strip_prefix("link-args=")) {
            (Some(arg), _) => vec![arg],
            (_, Some(args)) => args.split_whitespace().collect(),
            _ => Vec::new(),
        })
        .collect();
    assert!(
        link_args
            .iter()
            .any(|a| a.strip_prefix("-fuse-ld=").is_some_and(|ld| ld.contains("lld"))),
        "bitcode build: the linker must use lld (`-Clink-arg=-fuse-ld=lld`)"
    );
    let probe = out_dir.join("bitcode_lld_probe.c");
    fs::write(&probe, "int main(void) { return 0; }\n").unwrap();
    let probe_out = out_dir.join("bitcode_lld_probe").display().to_string();
    let mut args = link_args.clone();
    args.extend(["-Wl,--version", "-o", &probe_out]);
    let probe = probe.display().to_string();
    args.push(&probe);
    let lld_version = tool_output(&linker, &args);
    assert_eq!(
        Some(llvm),
        major_after(&lld_version, "LLD"),
        "bitcode build: lld must be LLVM {llvm}, the linker reports:\n{lld_version}"
    );

    let archiver = env::var_os("YANET_BITCODE_AR")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from(format!("llvm-ar-{llvm}")));
    tool_output(&archiver, &["--version"]);
    Toolchain { clang, archiver }
}
