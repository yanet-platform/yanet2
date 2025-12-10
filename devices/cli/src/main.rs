use std::{collections::HashSet, sync::LazyLock};

use clap::{Arg, ArgAction, ArgMatches, Command, crate_name, crate_version};
use colored::{ColoredString, Colorize};
use ync::dispatcher::{self, Dispatch};

static ERROR: LazyLock<ColoredString> = LazyLock::new(|| "error".bold().bright_red());

fn main() {
    dispatcher::dispatch(crate_name!(), "yanet-cli-device-", &Dispatcher);
}

struct Dispatcher;

impl Dispatch for Dispatcher {
    fn cmd(&self, modules: &HashSet<String>) -> Command {
        let command = Command::new(crate_name!())
            .version(crate_version!())
            .about("YANET device command dispatcher")
            .allow_external_subcommands(true)
            .subcommand(
                Command::new("list").about("List available device modules").arg(
                    Arg::new("verbose")
                        .long("verbose")
                        .short('v')
                        .action(ArgAction::Count)
                        .hide(true),
                ),
            );

        dispatcher::add_subcommands(command, modules)
    }

    fn try_match(&self, matches: &ArgMatches) -> Option<i32> {
        if let Some(("list", ..)) = matches.subcommand() {
            println!("To be implemented ...");
            return Some(1);
        }

        None
    }

    fn on_empty_subcommand(&self, modules: &HashSet<String>) -> i32 {
        print_empty_module_message_and_exit(modules);
        1
    }

    fn on_sub_binary_not_found(&self, subcommand: &str, modules: &HashSet<String>) {
        print_module_not_found_message(subcommand, modules);
    }
}

fn print_available_modules_message(modules: &HashSet<String>) {
    if modules.is_empty() {
        eprintln!(
            "{}: {}",
            "hint".bright_green(),
            "no device modules found on PATH".yellow()
        );
        return;
    }

    eprintln!(
        "{}: available device modules: {}",
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
    let cmd = subcommand.replace("yanet-cli-device-", "");
    eprintln!("{}: device module '{}' not found", *ERROR, cmd.yellow());
    eprintln!();
    eprintln!(
        "{}: binary '{}' is not found in any of paths described in '{}' environment variable",
        "hint".bright_green(),
        subcommand.yellow(),
        "PATH".yellow()
    );

    print_available_modules_message(modules);
}

fn print_empty_module_message_and_exit(modules: &HashSet<String>) {
    eprintln!("{}: no module specified", *ERROR);
    eprintln!();
    eprintln!("{}: {} <module>", "Usage".underline().bold(), crate_name!());
    eprintln!();
    print_available_modules_message(modules);
}
