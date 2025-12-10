use std::{
    collections::HashSet,
    env,
    error::Error,
    fs,
    io::ErrorKind,
    os::unix::{fs::MetadataExt, process::CommandExt},
    process::{self, Stdio},
};

use clap::{ArgMatches, Command};
use clap_complete::CompleteEnv;

pub trait Dispatch {
    /// Build the top-level CLI command.
    ///
    /// Called once before parsing to construct the CLI with dynamic
    /// subcommands.
    fn cmd(&self, modules: &HashSet<String>) -> Command;
    /// Handle already-parsed matches before the dispatcher falls through to a
    /// subcommand.
    ///
    /// Return `Some(code)` to stop dispatch.
    fn try_match(&self, matches: &ArgMatches) -> Option<i32>;
    /// Called when no subcommand was provided.
    ///
    /// Should print help/hint and return an exit code.
    fn on_empty_subcommand(&self, modules: &HashSet<String>) -> i32;
    /// Called when the target sub-binary was not found on `PATH`.
    fn on_sub_binary_not_found(&self, subcommand: &str, modules: &HashSet<String>);
}

/// Initializes the environment by setting the `PATH` environment variable.
///
/// This is necessary to correctly locate the submodule executable using a
/// relative path.
pub fn init_path() {
    let parent_path = match env::current_exe() {
        Ok(exe) => exe.parent().expect("must have parent path").to_path_buf(),
        Err(err) => {
            eprintln!("error: {}", err);
            process::exit(1);
        }
    };

    let path = env::var("PATH").unwrap_or_default();
    unsafe {
        // SAFETY: called from a single-thread application.
        env::set_var("PATH", format!("{}:{}", path, parent_path.display()));
    }
}

/// Locates executable binaries with prefix `prefix` in the `PATH` environment
/// variable.
pub fn locate_modules(prefix: &str) -> Result<HashSet<String>, Box<dyn Error>> {
    let mut modules = HashSet::new();
    for path in env::split_paths(&env::var_os("PATH").unwrap_or_default()) {
        if !path.is_dir() {
            continue;
        }

        for entry in fs::read_dir(path)? {
            let entry = entry?;
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
                if name.starts_with(prefix) && md.mode() & 0o200 == 0o200 {
                    modules.insert(name.replace(prefix, "").to_string());
                }
            }
        }
    }

    Ok(modules)
}

pub fn add_subcommands(mut command: Command, modules: &HashSet<String>) -> Command {
    let mut list = modules.iter().cloned().collect::<Vec<_>>();
    list.sort();
    for module in list {
        let name: &'static str = Box::leak(module.into_boxed_str());
        command = command.subcommand(Command::new(name).allow_external_subcommands(true));
    }

    command
}

pub fn try_complete(name: &str, prefix: &str, behavior: &impl Dispatch) {
    if env::var_os("COMPLETE").is_none() {
        return;
    }

    let submodules = locate_modules(prefix).unwrap_or_default();
    let args = env::args().collect::<Vec<_>>();

    // If args are ["<self>", "--", "<self>", "<module>", ..], forward the
    // completion request to the module binary.
    if args.len() >= 4 {
        let cmd = &args[3];

        if args[0].ends_with(name) && args[1] == "--" && args[2].ends_with(name) && submodules.contains(cmd) {
            let args = env::args().skip(4);

            let idx: u32 = match env::var("_CLAP_COMPLETE_INDEX") {
                Ok(idx) => match idx.parse() {
                    Ok(0) | Err(..) => {
                        return;
                    }
                    Ok(idx) => idx,
                },
                Err(..) => {
                    return;
                }
            };
            unsafe {
                // SAFETY: called from a single-thread application.
                env::set_var("_CLAP_COMPLETE_INDEX", format!("{}", idx - 1));
            }

            let subcommand = format!("{prefix}{cmd}");
            _ = process::Command::new(&subcommand)
                .arg("--")
                .arg(subcommand)
                .args(args)
                .stderr(Stdio::null())
                .exec();
            return;
        }
    }

    CompleteEnv::with_factory(|| behavior.cmd(&submodules)).complete();
}

pub fn dispatch(name: &str, prefix: &str, behavior: &impl Dispatch) -> ! {
    init_path();
    try_complete(name, prefix, behavior);

    let modules = locate_modules(prefix).unwrap_or_default();
    let matches = behavior.cmd(&modules).get_matches();

    if let Some(code) = behavior.try_match(&matches) {
        process::exit(code);
    }

    let (cmd, matches) = match matches.subcommand() {
        Some(v) => v,
        None => {
            let code = behavior.on_empty_subcommand(&modules);
            process::exit(code);
        }
    };

    let args = collect_args(matches);

    let subcommand = format!("{prefix}{cmd}");
    let err = process::Command::new(&subcommand).args(args).exec();

    match err.kind() {
        ErrorKind::NotFound => {
            behavior.on_sub_binary_not_found(&subcommand, &modules);
        }
        err => {
            eprintln!("error: {subcommand} - {err}");
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

    matches
        .get_raw("")
        .map(|v| v.map(|s| s.to_os_string()).collect::<Vec<_>>())
        .unwrap_or_default()
}
