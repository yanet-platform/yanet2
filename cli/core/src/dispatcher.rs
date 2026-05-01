use std::{
    collections::HashSet,
    env,
    error::Error,
    fs,
    io::ErrorKind,
    os::unix::{fs::PermissionsExt, process::CommandExt},
    path::PathBuf,
    process::{self, Stdio},
};

use clap::{builder::Str, Arg, ArgMatches, Command};
use clap_complete::CompleteEnv;

/// A nested dispatch namespace, e.g. `device` or `operator`.
///
/// When registered, top-level `yanet-cli <name> <child>` invocations are
/// routed to `yanet-cli-<name>-<child>`, and binaries matching that
/// extended prefix are removed from the top-level subcommand list to avoid
/// duplication.
pub struct Namespace {
    pub name: &'static str,
    pub about: &'static str,
}

pub trait Dispatch {
    /// Build the top-level CLI command.
    ///
    /// The dispatcher will append namespace subcommands and handle their
    /// children separately, so implementations should only register the
    /// flat set of `modules` here.
    fn cmd(&self, modules: &HashSet<String>) -> Command;
    /// Handle already-parsed matches before the dispatcher falls through to a
    /// subcommand.
    ///
    /// Return `Some(code)` to stop dispatch.
    fn try_match(&self, matches: &ArgMatches) -> Option<i32>;
    /// Called when no subcommand was provided at the top level.
    fn on_empty_subcommand(&self, modules: &HashSet<String>) -> i32;
    /// Called when a target sub-binary was not found on `PATH`.
    fn on_sub_binary_not_found(&self, subcommand: &str, modules: &HashSet<String>);
    /// Namespaces handled by this dispatcher.
    fn namespaces(&self) -> &[Namespace] {
        &[]
    }
    /// Called when no subcommand was provided after a namespace.
    ///
    /// Default delegates to `on_empty_subcommand`.
    fn on_empty_namespace(&self, _namespace: &str, modules: &HashSet<String>) -> i32 {
        self.on_empty_subcommand(modules)
    }
}

fn search_paths() -> Vec<PathBuf> {
    let mut paths = env::split_paths(&env::var_os("PATH").unwrap_or_default()).collect::<Vec<_>>();

    let parent = env::current_exe()
        .ok()
        .and_then(|exe| exe.parent().map(|v| v.to_path_buf()));

    if let Some(parent) = parent {
        paths.push(parent);
    }

    paths
}

/// Locates executable binaries with prefix `prefix` in the `PATH` environment
/// variable.
pub fn locate_modules(prefix: &str) -> Result<HashSet<String>, Box<dyn Error>> {
    let mut modules = HashSet::new();
    for path in search_paths() {
        if !path.is_dir() {
            continue;
        }

        let entries = match fs::read_dir(&path) {
            Ok(entries) => entries,
            Err(err) => {
                // Continue on error, because some directories may not be readable.
                log::trace!("failed to read directory {}: {err}", path.display());
                continue;
            }
        };
        for entry in entries {
            let entry = match entry {
                Ok(entry) => entry,
                Err(err) => {
                    // Continue on error, because some entries may not be readable.
                    log::trace!("failed to read entry in directory {}: {err}", path.display());
                    continue;
                }
            };
            let path = entry.path();

            if !path.is_file() {
                continue;
            }

            // Skip binaries with extension (but preserve Windows case).
            match path.extension() {
                Some(ext) if ext == "exe" => {}
                None => {}
                Some(..) => continue,
            }

            let Some(name) = path.file_name().and_then(|v| v.to_str()) else {
                continue;
            };

            if let Ok(md) = path.metadata() {
                if let Some(name) = name.strip_prefix(prefix) {
                    if md.permissions().mode() & 0o111 != 0 {
                        modules.insert(name.to_string());
                    }
                }
            }
        }
    }

    Ok(modules)
}

fn locate_sub_binary(prefix: &str, subcommand: &str) -> Option<PathBuf> {
    let subcommand = format!("{prefix}{subcommand}");

    for path in search_paths() {
        let path = path.join(&subcommand);
        if path.is_file() {
            return Some(path);
        }
    }

    None
}

pub fn add_subcommands(mut command: Command, modules: &HashSet<String>) -> Command {
    let mut list = modules.iter().cloned().collect::<Vec<_>>();
    list.sort();
    for module in list {
        command = command.subcommand(external_subcommand(module));
    }

    command
}

/// Creates a subcommand that forwards all arguments to an external binary.
///
/// By default, clap adds `-h/--help` flags to every subcommand and handles
/// them internally, showing an empty help message instead of forwarding to
/// the actual module binary. To fix this:
///
/// 1. We disable clap's built-in help handling so `-h`/`--help` are not
///    intercepted.
/// 2. We add a catch-all `args` argument with `allow_hyphen_values(true)` to
///    accept flags like `-h`, `--help`, `-v`, etc.
/// 3. We use `trailing_var_arg(true)` to capture all remaining arguments.
///
/// This ensures that, for example, `yanet-cli inspect -h` behaves the same as
/// calling `yanet-cli-inspect -h` directly.
fn external_subcommand(name: impl Into<Str>) -> Command {
    Command::new(name)
        .disable_help_flag(true)
        .disable_help_subcommand(true)
        .arg(
            Arg::new("args")
                .num_args(..)
                .allow_hyphen_values(true)
                .trailing_var_arg(true),
        )
}

/// Removes module names that belong to one of the registered namespaces
/// from the top-level set, e.g. drops `device` and `device-plain` when
/// `device` is a namespace. Excluding the bare namespace name avoids a
/// clap subcommand collision when a stale `yanet-cli-<ns>` binary is left
/// over on `PATH`.
fn filter_namespaced(modules: &HashSet<String>, namespaces: &[Namespace]) -> HashSet<String> {
    modules
        .iter()
        .filter(|name| {
            !namespaces.iter().any(|ns| {
                name.strip_prefix(ns.name)
                    .is_some_and(|rest| rest.is_empty() || rest.starts_with('-'))
            })
        })
        .cloned()
        .collect()
}

pub fn try_complete(name: &str, prefix: &str, behavior: &impl Dispatch) {
    if env::var_os("COMPLETE").is_none() {
        return;
    }

    let raw = locate_modules(prefix).unwrap_or_default();
    let namespaces = behavior.namespaces();
    let submodules = filter_namespaced(&raw, namespaces);
    let args = env::args().collect::<Vec<_>>();

    // If args are ["<self>", "--", "<self>", "<module>", ..], forward the
    // completion request to the module binary.
    if args.len() >= 4 {
        let cmd = &args[3];

        if args[0].ends_with(name) && args[1] == "--" && args[2].ends_with(name) {
            // Direct module: "yanet-cli <module> ..."
            if submodules.contains(cmd) {
                forward_completion(prefix, cmd, env::args().skip(4), 1);
                return;
            }
            // Namespaced: "yanet-cli <namespace> <module> ..."
            if let Some(ns) = namespaces.iter().find(|ns| ns.name == cmd) {
                if let Some(child) = args.get(4) {
                    let ns_prefix = format!("{prefix}{}-", ns.name);
                    let children = locate_modules(&ns_prefix).unwrap_or_default();
                    if children.contains(child) {
                        forward_completion(&ns_prefix, child, env::args().skip(5), 2);
                        return;
                    }
                }
            }
        }
    }

    CompleteEnv::with_factory(|| build_command(behavior, &submodules, namespaces, prefix)).complete();
}

/// Forwards a shell completion request to a sub-binary, decrementing the
/// cursor index by `consumed` to account for positional args already
/// resolved at this level (one for direct modules, two for namespaced).
fn forward_completion(prefix: &str, cmd: &str, args: impl Iterator<Item = String>, consumed: u32) {
    let idx: u32 = match env::var("_CLAP_COMPLETE_INDEX")
        .ok()
        .and_then(|v| v.parse::<u32>().ok())
    {
        Some(idx) if idx > consumed => idx - consumed,
        _ => return,
    };

    let Some(subcommand) = locate_sub_binary(prefix, cmd) else {
        return;
    };
    let subcommand_name = format!("{prefix}{cmd}");

    _ = process::Command::new(&subcommand)
        .arg("--")
        .arg(subcommand_name)
        .args(args)
        .stderr(Stdio::null())
        .env("_CLAP_COMPLETE_INDEX", format!("{idx}"))
        .exec();
}

fn build_command(
    behavior: &impl Dispatch,
    modules: &HashSet<String>,
    namespaces: &[Namespace],
    prefix: &str,
) -> Command {
    let mut cmd = behavior.cmd(modules);
    for ns in namespaces {
        let children = locate_modules(&format!("{prefix}{}-", ns.name)).unwrap_or_default();
        let sub = Command::new(ns.name).about(ns.about).allow_external_subcommands(true);
        cmd = cmd.subcommand(add_subcommands(sub, &children));
    }
    cmd
}

pub fn dispatch(name: &str, prefix: &str, behavior: &impl Dispatch) -> ! {
    try_complete(name, prefix, behavior);

    let namespaces = behavior.namespaces();
    let raw = locate_modules(prefix).unwrap_or_default();
    let modules = filter_namespaced(&raw, namespaces);

    let matches = build_command(behavior, &modules, namespaces, prefix).get_matches();

    if let Some(code) = behavior.try_match(&matches) {
        process::exit(code);
    }

    let (cmd, sub_matches) = match matches.subcommand() {
        Some(v) => v,
        None => process::exit(behavior.on_empty_subcommand(&modules)),
    };

    // Resolve the final (prefix, cmd, args, modules) tuple, recursing once
    // into the namespace level if the top-level subcommand is a namespace.
    let (prefix, cmd, sub_matches, modules) = if let Some(ns) = namespaces.iter().find(|ns| ns.name == cmd) {
        let ns_prefix = format!("{prefix}{}-", ns.name);
        let children = locate_modules(&ns_prefix).unwrap_or_default();
        let (cmd, sub_matches) = match sub_matches.subcommand() {
            Some(v) => v,
            None => process::exit(behavior.on_empty_namespace(ns.name, &children)),
        };
        (ns_prefix, cmd, sub_matches, children)
    } else {
        (prefix.to_string(), cmd, sub_matches, modules)
    };

    exec_sub_binary(&prefix, cmd, collect_args(sub_matches), &modules, behavior);
}

fn exec_sub_binary(
    prefix: &str,
    cmd: &str,
    args: Vec<std::ffi::OsString>,
    modules: &HashSet<String>,
    behavior: &impl Dispatch,
) -> ! {
    let subcommand = format!("{prefix}{cmd}");
    let Some(path) = locate_sub_binary(prefix, cmd) else {
        behavior.on_sub_binary_not_found(&subcommand, modules);
        process::exit(1);
    };

    let err = process::Command::new(&path).args(args).exec();

    match err.kind() {
        ErrorKind::NotFound => {
            behavior.on_sub_binary_not_found(&subcommand, modules);
        }
        err => {
            eprintln!("error: {} - {err}", path.display());
        }
    }

    process::exit(1);
}

fn collect_args(matches: &ArgMatches) -> Vec<std::ffi::OsString> {
    if let Some((sub, sub_matches)) = matches.subcommand() {
        let mut args = Vec::new();
        args.push(std::ffi::OsString::from(sub));
        args.extend(collect_args(sub_matches));
        return args;
    }

    // Try named "args" first (from external_subcommand), then fallback to
    // external subcommand args.
    if let Some(raw) = matches.get_raw("args") {
        return raw.map(|s| s.to_os_string()).collect();
    }

    matches
        .get_raw("")
        .map(|v| v.map(|s| s.to_os_string()).collect::<Vec<_>>())
        .unwrap_or_default()
}
