//! CLI for YANET pipeline operator.
//!
//! A placeholder until the operator grows intent RPCs: it carries the
//! shared connection and output flags and no commands, so every
//! invocation ends in help or a usage error. Operator metrics are read
//! with `yanet-cli metrics`.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use ync::{client::ConnectionArgs, output::CommonFormat};

/// Pipeline operator CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true, subcommand_required = true, arg_required_else_help = true)]
pub struct Cmd {
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    Cmd::parse();
}
