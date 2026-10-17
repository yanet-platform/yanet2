//! Compile flags of the configured meson build, for build scripts that compile
//! C against the YANET headers.
//!
//! The flags are taken from the meson compilation database entry of the C
//! decap dataplane, so C compiled here sees exactly the defines, include
//! directories and forced includes the dataplane itself is built with.

use std::{
    env, fs,
    path::{Path, PathBuf},
};

/// Source whose meson compile command supplies the flags.
const REFERENCE_SOURCE: &str = "modules/decap/dataplane/dataplane.c";

/// Environment variable overriding the meson build directory.
pub const BUILD_DIR_ENV: &str = "YANET_BUILD_DIR";

/// C compile flags of one meson build directory.
#[derive(Debug)]
pub struct MesonFlags {
    /// Repository root.
    pub repo_root: PathBuf,
    /// Meson build directory the flags were read from.
    pub build_dir: PathBuf,
    /// Include directories in command-line order, made absolute.
    pub include_dirs: Vec<PathBuf>,
    /// Preprocessor defines, `NAME` or `NAME=VALUE`.
    pub defines: Vec<(String, Option<String>)>,
    /// Remaining flags that matter for layout: forced includes and `-march`.
    pub flags: Vec<String>,
}

/// Repository root, three levels above this crate.
pub fn repo_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../..")
        .canonicalize()
        .expect("repository root must exist")
}

/// Reads the flags from `$YANET_BUILD_DIR` or `<repo>/build`.
///
/// Fails with a message naming the setup command when the build directory has
/// not been configured.
pub fn meson_flags() -> Result<MesonFlags, String> {
    let repo_root = repo_root();
    let build_dir = env::var_os(BUILD_DIR_ENV)
        .map(PathBuf::from)
        .unwrap_or_else(|| repo_root.join("build"));
    let database = build_dir.join("compile_commands.json");
    let text = fs::read_to_string(&database).map_err(|err| {
        format!(
            "cannot read {}: {err}; run `meson setup build` in {} or set {BUILD_DIR_ENV}",
            database.display(),
            repo_root.display()
        )
    })?;
    let command = reference_command(&text)
        .ok_or_else(|| format!("{} has no entry for {REFERENCE_SOURCE}", database.display()))?;

    let mut flags = MesonFlags {
        repo_root,
        build_dir: build_dir.clone(),
        include_dirs: Vec::new(),
        defines: Vec::new(),
        flags: Vec::new(),
    };
    let mut args = command.split_whitespace();
    while let Some(arg) = args.next() {
        if let Some(dir) = arg.strip_prefix("-I") {
            flags.include_dirs.push(build_dir.join(dir));
        } else if let Some(define) = arg.strip_prefix("-D") {
            let (name, value) = match define.split_once('=') {
                Some((name, value)) => (name.to_string(), Some(value.to_string())),
                None => (define.to_string(), None),
            };
            flags.defines.push((name, value));
        } else if arg == "-include" {
            let header = args.next().ok_or("dangling -include in the compile command")?;
            flags.flags.push(format!("-include{header}"));
        } else if arg.starts_with("-march=") {
            flags.flags.push(arg.to_string());
        }
    }
    Ok(flags)
}

/// Extracts the `command` string of the reference source's entry.
///
/// The database is a flat JSON array written by meson, with the source path
/// relative to the build directory wherever that directory lives; a full
/// JSON parser is not needed to find one entry and its unescaped command.
fn reference_command(text: &str) -> Option<&str> {
    const FILE_KEY: &str = "\"file\": \"";
    let mut rest = text;
    let file_at = loop {
        let at = rest.find(FILE_KEY)? + FILE_KEY.len();
        let value = &rest[at..rest[at..].find('"')? + at];
        if value == REFERENCE_SOURCE || value.ends_with(&format!("/{REFERENCE_SOURCE}")) {
            break text.len() - rest.len() + at;
        }
        rest = &rest[at..];
    };
    let entry_start = text[..file_at].rfind('{')?;
    let entry = &text[entry_start..file_at];
    let command_at = entry.find("\"command\": \"")? + "\"command\": \"".len();
    let command = &entry[command_at..];
    let end = command.find("\",")?;
    Some(&command[..end])
}
