use std::{
    collections::HashSet,
    env,
    error::Error,
    fs,
    io::ErrorKind,
    os::unix::{fs::MetadataExt, process::CommandExt},
    path,
    process::{self, Command},
    sync::LazyLock,
};

use colored::{ColoredString, Colorize};

static ERROR: LazyLock<ColoredString> = LazyLock::new(|| "error".bold().bright_red());

fn main() {
    let mut args = std::env::args();

    let parent_path = match args.next() {
        Some(arg) => path::absolute(arg)
            .expect("must have absolute path")
            .parent()
            .expect("must have parent path")
            .to_path_buf(),
        None => {
            eprintln!("{}: no argv[0]?", *ERROR);
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

    let modules = find_modules().unwrap();

    let module = match args.next() {
        Some(arg) => arg,
        None => {
            eprintln!("{}: no module specified", *ERROR);
            eprintln!();
            eprintln!("{}: yanet-cli <module>", "Usage".underline().bold());
            eprintln!();
            eprintln!(
                "{}: available modules: {}",
                "hint".bright_green(),
                modules
                    .iter()
                    .map(|m| m.as_str())
                    .collect::<Vec<_>>()
                    .as_slice()
                    .join(", ")
                    .yellow(),
            );

            process::exit(1);
        }
    };

    let subcommand = format!("yanet-cli-{}", module);
    let err = Command::new(&subcommand).args(args).exec();

    match err.kind() {
        ErrorKind::NotFound => {
            eprintln!("{}: module '{}' not found", *ERROR, module.yellow());
            eprintln!();
            eprintln!(
                "{}: binary '{}' is not found in any of paths described in '{}' environment variable",
                "hint".bright_green(),
                subcommand.yellow(),
                "PATH".yellow()
            )
        }
        err => {
            eprintln!("{}: {subcommand} - {err}", *ERROR);
        }
    }

    process::exit(1);
}

/// Finds executable binaries with prefix "yanet-cli-" in the "PATH" environment
/// variable.
fn find_modules() -> Result<HashSet<String>, Box<dyn Error>> {
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
                                modules.insert(name.to_string());
                            }
                        }
                    }
                }
            }
        }
    }

    Ok(modules)
}
