use std::{collections::HashSet, sync::LazyLock};

use clap::{crate_name, crate_version, ArgMatches, Command};
use colored::{ColoredString, Colorize};
use yanet_cli::dispatcher::{self, Dispatch};

static ERROR: LazyLock<ColoredString> = LazyLock::new(|| "error".bold().bright_red());

fn main() {
    dispatcher::dispatch(crate_name!(), "yanet-cli-", &Dispatcher);
}

struct Dispatcher;

impl Dispatch for Dispatcher {
    fn cmd(&self, modules: &HashSet<String>) -> Command {
        let cmd = Command::new(crate_name!())
            .version(crate_version!())
            .allow_external_subcommands(true);
        dispatcher::add_subcommands(cmd, modules)
    }

    fn try_match(&self, _matches: &ArgMatches) -> Option<i32> {
        None
    }

    fn on_empty_subcommand(&self, modules: &HashSet<String>) -> i32 {
        print_empty_module_message(modules);
        1
    }

    fn on_sub_binary_not_found(&self, subcommand: &str, modules: &HashSet<String>) {
        print_module_not_found_message(subcommand, modules);
    }
}

fn print_empty_module_message(modules: &HashSet<String>) {
    eprintln!("{}: no module specified", *ERROR);
    eprintln!();
    eprintln!("{}: {} <module>", "Usage".underline().bold(), crate_name!());
    eprintln!();
    print_available_modules_message(modules.clone());
}

fn print_available_modules_message(modules: HashSet<String>) {
    let mut modules = modules
        .iter()
        .map(|m| m.as_str().yellow().to_string())
        .collect::<Vec<_>>();
    modules.sort();

    eprintln!(
        "{}: available modules: {}",
        "hint".bright_green(),
        modules.iter().as_slice().join(", ")
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

    print_available_modules_message(modules.clone());
}
