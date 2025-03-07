use std::{
    collections::HashSet,
    env,
    error::Error,
    ffi::OsString,
    fs,
    io::ErrorKind,
    os::unix::{fs::MetadataExt, process::CommandExt},
    process::{self, Stdio},
    sync::LazyLock,
};

use clap::{crate_name, crate_version, Command};
use clap_complete::CompleteEnv;
use colored::{ColoredString, Colorize};

static ERROR: LazyLock<ColoredString> = LazyLock::new(|| "error".bold().bright_red());

fn main() {
    init();
    try_complete();

    let modules = locate_modules().unwrap_or_default();
    let matches = cmd().get_matches();

    let (cmd, matches) = match matches.subcommand() {
        Some(v) => v,
        None => {
            // No submodule was provided.
            //
            // Print hint message with the list of available modules and exit.
            print_empty_module_message_and_exit(&modules);
        }
    };

    let args = matches.get_many::<OsString>("").expect("expected downcast to OsString");

    let subcommand = format!("yanet-cli-{}", cmd);
    let err = process::Command::new(&subcommand).args(args).exec();

    match err.kind() {
        ErrorKind::NotFound => {
            print_module_not_found_message(&subcommand, &modules);
        }
        err => {
            eprintln!("{}: {subcommand} - {err}", *ERROR);
        }
    }

    // In case of successful `exec` we can't be here. Hence, non-zero exit code.
    process::exit(1);
}

/// Initializes the environment by setting the `PATH` environment variable.
///
/// This is necessary to correctly locate the submodule executable using a
/// relative path.
fn init() {
    let parent_path = match env::current_exe() {
        Ok(exe) => exe.parent().expect("must have parent path").to_path_buf(),
        Err(err) => {
            eprintln!("{}: {}", *ERROR, err);
            process::exit(1);
        }
    };

    // Append the path of the "yanet-cli" binary to the "PATH" environment
    // variable.
    let path = env::var("PATH").unwrap_or_default();
    unsafe {
        // SAFETY: called from a single-thread application.
        env::set_var("PATH", format!("{}:{}", path, parent_path.display()));
    }
}

/// Constructs the CLI command.
fn cmd() -> Command {
    Command::new(crate_name!())
        .version(crate_version!())
        .allow_external_subcommands(true)
}

/// Locates executable binaries with prefix "yanet-cli-" in the "PATH"
/// environment variable.
fn locate_modules() -> Result<HashSet<String>, Box<dyn Error>> {
    let mut modules = HashSet::new();
    for path in env::split_paths(&env::var_os("PATH").unwrap_or_default()) {
        if !path.is_dir() {
            continue;
        }

        for entry in fs::read_dir(path)? {
            let entry = entry?;
            let path = entry.path();

            if path.is_file() {
                // Skip binaries with extension (but preserve Windows case).
                match path.extension() {
                    Some(ext) if ext == "exe" => {}
                    None => {}
                    Some(..) => {
                        continue;
                    }
                }

                if let Some(name) = path.file_name() {
                    if let Some(name) = name.to_str() {
                        if let Ok(md) = path.metadata() {
                            if name.starts_with("yanet-cli-") && md.mode() & 0o200 == 0o200 {
                                modules.insert(name.replace("yanet-cli-", "").to_string());
                            }
                        }
                    }
                }
            }
        }
    }

    Ok(modules)
}

fn print_empty_module_message_and_exit(modules: &HashSet<String>) -> ! {
    eprintln!("{}: no module specified", *ERROR);
    eprintln!();
    eprintln!("{}: {} <module>", "Usage".underline().bold(), crate_name!());
    eprintln!();
    print_available_modules_message(modules);

    process::exit(1);
}

fn print_available_modules_message(modules: &HashSet<String>) {
    eprintln!(
        "{}: available modules: {}",
        "hint".bright_green(),
        modules
            .iter()
            .map(|m| m.as_str().yellow().to_string())
            .collect::<Vec<_>>()
            .as_slice()
            .join(", ")
    );
}

fn print_module_not_found_message(subcommand: &str, modules: &HashSet<String>) {
    let cmd = subcommand.replace("yanet-cli-", "");

    eprintln!("{}: module '{}' not found", *ERROR, cmd.yellow());
    eprintln!();
    eprintln!(
        "{}: binary '{}' is not found in any of paths described in '{}' environment variable",
        "hint".bright_green(),
        subcommand.yellow(),
        "PATH".yellow()
    );

    print_available_modules_message(modules);
}

fn try_complete() {
    if env::var_os("COMPLETE").is_none() {
        return;
    }

    let submodules = locate_modules().unwrap_or_default();

    // If args are ["yanet-cli", "--", "yanet-cli", "<module>", ..], then we
    // need to forward the completion request.
    let args = env::args().collect::<Vec<_>>();

    if args.len() >= 4 {
        let cmd = &args[3];

        if args[0].ends_with(crate_name!())
            && args[1] == "--"
            && args[2].ends_with(crate_name!())
            && submodules.contains(cmd)
        {
            let args = env::args().skip(4);

            // Decrement special environment variable.
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

            // Invoke submodule completion.
            let subcommand = format!("{}-{}", crate_name!(), cmd);
            _ = process::Command::new(&subcommand)
                .arg("--")
                .arg(subcommand)
                .args(args)
                .stderr(Stdio::null())
                .exec();
            return;
        }
    }

    complete();
}

fn complete() {
    let submodules = locate_modules().unwrap_or_default();

    CompleteEnv::with_factory(|| {
        cmd().subcommands(
            submodules
                .clone()
                .into_iter()
                .map(|v| Command::new(&*Box::leak(v.into_boxed_str()))),
        )
    })
    .complete();
}
