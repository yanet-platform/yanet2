use std::{collections::HashSet, sync::LazyLock};

use clap::{crate_name, crate_version, ArgMatches, Command};
use colored::{ColoredString, Colorize};
use yanet_cli::dispatcher::{self, Dispatch, Namespace};

static ERROR: LazyLock<ColoredString> = LazyLock::new(|| "error".bold().bright_red());

const NAMESPACES: &[Namespace] = &[
    Namespace {
        name: "device",
        about: "YANET device command dispatcher",
    },
    Namespace {
        name: "operator",
        about: "YANET operator command dispatcher",
    },
];

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
        let mut all = modules.clone();
        for ns in NAMESPACES {
            all.insert(ns.name.to_string());
        }
        print_empty_message(None, &all);
        1
    }

    fn on_empty_namespace(&self, namespace: &str, modules: &HashSet<String>) -> i32 {
        print_empty_message(Some(namespace), modules);
        1
    }

    fn on_sub_binary_not_found(&self, subcommand: &str, modules: &HashSet<String>) {
        print_module_not_found_message(subcommand, modules);
    }

    fn namespaces(&self) -> &[Namespace] {
        NAMESPACES
    }
}

fn print_empty_message(namespace: Option<&str>, modules: &HashSet<String>) {
    let infix = namespace.map(|ns| format!("{ns} ")).unwrap_or_default();
    eprintln!("{}: no {infix}module specified", *ERROR);
    eprintln!();
    eprintln!("{}: {} {infix}<module>", "Usage".underline().bold(), crate_name!());
    eprintln!();
    print_available_modules_message(namespace, modules);
}

fn print_available_modules_message(namespace: Option<&str>, modules: &HashSet<String>) {
    let kind = namespace.map(|ns| format!("{ns} ")).unwrap_or_default();

    if modules.is_empty() {
        eprintln!(
            "{}: {}",
            "hint".bright_green(),
            format!("no {kind}modules found on PATH").yellow()
        );
        return;
    }

    let mut modules = modules
        .iter()
        .map(|m| m.as_str().yellow().to_string())
        .collect::<Vec<_>>();
    modules.sort();

    eprintln!(
        "{}: available {kind}modules: {}",
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

    print_available_modules_message(None, modules);
}
