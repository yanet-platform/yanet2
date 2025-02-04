use std::{
    env,
    io::ErrorKind,
    os::unix::process::CommandExt,
    path,
    process::{self, Command},
    sync::LazyLock,
};

use colored::{ColoredString, Colorize};

const ERROR: LazyLock<ColoredString> = LazyLock::new(|| "error".bold().bright_red());

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

    let module = match args.next() {
        Some(arg) => arg,
        None => {
            eprintln!("Usage: yanet-cli <module>");
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
