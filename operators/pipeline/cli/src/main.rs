//! CLI for YANET pipeline operator.
//!
//! A placeholder until the operator grows intent RPCs: it carries the
//! shared connection and output flags and no commands, so every
//! invocation ends in help or a usage error. Operator metrics are read
//! with `yanet-cli metrics`.

use clap::Parser;
use ync::{GlobalArgs, errors::Error};

/// Manages the pipeline operator (no commands yet).
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true, subcommand_required = true, arg_required_else_help = true)]
pub struct Cmd {
    #[command(flatten)]
    pub globals: GlobalArgs,
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

async fn run(_: Cmd) -> Result<(), Error> {
    Ok(())
}
