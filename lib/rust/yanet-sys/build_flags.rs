//! The C flags of the dataplane, as meson's compilation database has them.
//!
//! Read by the build script of the bindings crate. The inputs are the
//! database in the directory `YANET_BUILD_DIR` names and the C headers of the
//! source tree.

use std::{
    env, fs,
    path::{Path, PathBuf},
};

use serde_json::Value;

/// Environment variable naming the meson build directory.
pub const BUILD_DIR_ENV: &str = "YANET_BUILD_DIR";

/// Private directory of the target that builds the packet library, whose
/// objects' compile command the bindings follow: it compiles against DPDK,
/// and its sources are the ones the shim builds into the plugin.
const TARGET_DIR: &str = "/libpacket.a.p/";

/// Options, matched by prefix, that change the layout of the types or the
/// predefined macros the headers read. bindgen must lay the types out as the C
/// compiler does, so none of them may be dropped.
const ABI_OPTIONS: &[&str] = &[
    "-std=",
    "-march=",
    "-mabi=",
    "-m32",
    "-m64",
    "-mx32",
    "-malign-double",
    "-mno-align-double",
    "-fpack-struct",
    "-fshort-enums",
    "-fno-short-enums",
    "-fshort-wchar",
    "-fno-short-wchar",
    "-fsigned-char",
    "-funsigned-char",
];

/// Repository directories whose C headers the bindings and the shim read,
/// DPDK's among them (`rte_mbuf_core.h` and what the routing helpers reach).
const C_HEADER_DIRS: &[&str] = &[
    "common",
    "lib/controlplane",
    "lib/counters",
    "lib/dataplane",
    "lib/errors",
    "subprojects/dpdk/config",
    "subprojects/dpdk/lib",
];

/// DPDK's generated configuration header, relative to the build directory.
const DPDK_BUILD_CONFIG: &str = "subprojects/dpdk/rte_build_config.h";

/// The generated YANET build configuration the bindings include.
const YANET_BUILD_CONFIG: &str = "yanet_build_config.h";

/// First tokens of a compiler command that wrap the compiler.
const COMPILER_WRAPPERS: &[&str] = &["ccache", "sccache", "distcc", "icecc"];

/// Options that name an include directory, given joined or as the next
/// argument.
const INCLUDE_DIR_OPTIONS: &[&str] = &["-I", "-isystem", "-iquote"];

/// Options that name a file or directory that is not searched.
const PATH_OPTIONS: &[&str] = &["-include", "--sysroot"];

/// Options with a value that is not a path.
const VALUE_OPTIONS: &[&str] = &["-D", "-U"];

/// Options that pass an argument to another tool, not warning options.
const TOOL_OPTIONS: &[&str] = &["-Wa,", "-Wl,", "-Wp,"];

/// Compiler and header options of the dataplane's C sources.
#[derive(Debug, PartialEq, Eq)]
pub struct CFlags {
    /// Repository root: the directory the C sources and headers are under.
    pub root: PathBuf,
    pub compiler: String,
    /// The options that change how headers are read, in command order. A
    /// value follows its option as an argument of its own, except in the
    /// joined ABI options such as `-std=`; paths are absolute, but for a
    /// forced include not found relative to the build directory, which the
    /// compiler finds through the include directories.
    args: Vec<String>,
    /// The warning options, `-Werror` among them: the shim compiles with the
    /// warnings the C build does. Kept apart because the bindings are read
    /// by libclang, which is no judge of the headers.
    warnings: Vec<String>,
}

impl CFlags {
    /// Reads the compilation database in the directory `YANET_BUILD_DIR`
    /// names, and asks cargo to rerun the build script when the variable or
    /// the database changes.
    pub fn load(root: PathBuf) -> Self {
        println!("cargo:rerun-if-env-changed={BUILD_DIR_ENV}");
        let dir = env::var_os(BUILD_DIR_ENV).map(PathBuf::from).unwrap_or_else(|| {
            panic!(
                "{BUILD_DIR_ENV} names no meson build directory: run `meson setup build` at the repository root, or \
                 set {BUILD_DIR_ENV}"
            )
        });
        let path = dir.join("compile_commands.json");
        println!("cargo:rerun-if-changed={}", path.display());
        println!("cargo:rerun-if-changed={}", dir.join(DPDK_BUILD_CONFIG).display());
        println!("cargo:rerun-if-changed={}", dir.join(YANET_BUILD_CONFIG).display());
        let text = fs::read_to_string(&path).unwrap_or_else(|err| {
            panic!(
                "cannot read {}: {err} (run `meson setup build` at the repository root, or set {BUILD_DIR_ENV})",
                path.display()
            )
        });
        Self::parse(root, &text).unwrap_or_else(|err| panic!("{}: {err}", path.display()))
    }

    /// Takes the flags from the entry of a compilation database that builds
    /// an object of the packet library.
    pub fn parse(root: PathBuf, text: &str) -> Result<Self, String> {
        let database: Value = serde_json::from_str(text).map_err(|err| format!("not a compilation database: {err}"))?;
        let entries = database.as_array().ok_or("not a list of entries")?;
        let (directory, tokens) = entries
            .iter()
            .find_map(|entry| {
                let directory = Path::new(entry["directory"].as_str()?);
                entry["output"]
                    .as_str()?
                    .contains(TARGET_DIR)
                    .then(|| (directory, command_tokens(entry)))
            })
            .ok_or_else(|| format!("no entry builds an object under {TARGET_DIR}"))?;
        let mut tokens = tokens?.into_iter();
        let compiler = tokens
            .by_ref()
            .find(|token| !COMPILER_WRAPPERS.contains(&file_name(token)))
            .ok_or("the command names no compiler")?;
        let (args, warnings) = reduce(directory, tokens);
        Ok(Self { root, compiler, args, warnings })
    }

    /// Flags that reproduce how the dataplane reads headers.
    pub fn header_args(&self) -> &[String] {
        &self.args
    }

    /// The warning options of the dataplane's C sources.
    pub fn warning_args(&self) -> &[String] {
        &self.warnings
    }

    /// Path inside the repository.
    pub fn source(&self, relative: impl AsRef<Path>) -> PathBuf {
        self.root.join(relative)
    }

    /// Asks cargo to rerun the build script when a C header the bindings
    /// read changes; cargo scans each directory.
    pub fn rerun_on_headers(&self) {
        for dir in C_HEADER_DIRS {
            println!("cargo:rerun-if-changed={}", self.source(dir).display());
        }
    }
}

fn file_name(token: &str) -> &str {
    Path::new(token)
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or(token)
}

/// The command of an entry: its `arguments` list, or its `command` split as
/// a shell would.
fn command_tokens(entry: &Value) -> Result<Vec<String>, String> {
    if let Some(arguments) = entry["arguments"].as_array() {
        return arguments
            .iter()
            .map(|argument| {
                argument
                    .as_str()
                    .map(str::to_owned)
                    .ok_or("`arguments` holds a non-string".to_owned())
            })
            .collect();
    }
    let command = entry["command"]
        .as_str()
        .ok_or("the entry has neither `command` nor `arguments`")?;
    shlex::split(command).ok_or_else(|| "the command is not a valid shell command".to_owned())
}

/// Splits the options into the ones that change how headers are read and the
/// warning options, dropping the rest. Paths are resolved
/// against the directory the command ran in; include directories are
/// canonicalised and, when missing, dropped: meson adds per-target private
/// directories that may not exist after setup and hold no header.
fn reduce(directory: &Path, tokens: impl Iterator<Item = String>) -> (Vec<String>, Vec<String>) {
    let resolve = |path: &str| directory.join(path);
    let mut tokens = tokens;
    let (mut args, mut warnings) = (Vec::new(), Vec::new());
    while let Some(token) = tokens.next() {
        let option = INCLUDE_DIR_OPTIONS
            .iter()
            .chain(PATH_OPTIONS)
            .chain(VALUE_OPTIONS)
            .find_map(|&option| token.strip_prefix(option).map(|joined| (option, joined)));
        let Some((option, joined)) = option else {
            if ABI_OPTIONS.iter().any(|&option| token.starts_with(option)) {
                args.push(token);
            } else if token.starts_with("-W") && !TOOL_OPTIONS.iter().any(|tool| token.starts_with(tool)) {
                warnings.push(token);
            }
            continue;
        };
        // `--sysroot=path` is the joined form of its option.
        let joined = joined
            .strip_prefix('=')
            .filter(|_| option == "--sysroot")
            .unwrap_or(joined);
        let Some(value) = (if joined.is_empty() {
            tokens.next()
        } else {
            Some(joined.to_owned())
        }) else {
            continue;
        };
        let value = if INCLUDE_DIR_OPTIONS.contains(&option) {
            match fs::canonicalize(resolve(&value)) {
                Ok(dir) => dir.display().to_string(),
                Err(_) => continue,
            }
        } else if PATH_OPTIONS.contains(&option) {
            // A forced include that is not a file there is found through the
            // include directories, as the compiler does.
            let path = resolve(&value);
            if path.exists() {
                path.display().to_string()
            } else {
                value
            }
        } else {
            value
        };
        args.extend([option.to_owned(), value]);
    }
    (args, warnings)
}
