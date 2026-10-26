use core::{future::Future, pin::Pin};
use std::process::{ExitCode, Termination};

use clap::{ArgAction, ArgMatches, Args, Command, Parser};
use clap_complete::CompleteEnv;

use crate::{
    client::ConnectionArgs,
    errors::Error,
    output::{CommonFormat, Format, GlobalFormat},
};

pub mod auth;
pub mod client;
pub mod completion;
pub mod config;
pub mod discovery;
pub mod dispatcher;
pub mod display;
pub mod errors;
pub mod humanfmt;
pub mod logging;
pub mod metrics;
pub mod output;
pub mod progress;
pub mod timeout;
pub mod yaml;

/// The gRPC call path the generated clients share.
pub use ync_rpc as rpc;

#[cfg(unix)]
mod pager;
mod signal;

/// The flags every yanet CLI carries: the connection, the output format and
/// the verbosity.
///
/// They also carry the switch that turns the pager off.
#[derive(Debug, Clone, Args)]
pub struct GlobalArgs {
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[arg(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
    /// Print long output directly instead of through the pager.
    #[arg(long, global = true)]
    pub no_pager: bool,
}

impl GlobalArgs {
    /// The verbosity and format pair [`entrypoint`] initialises the output
    /// with.
    pub fn options(&self) -> (u8, GlobalFormat) {
        let format = GlobalFormat {
            format: self.format,
            pager: !self.no_pager,
        };

        (self.verbose, format)
    }
}

/// Initialise the logger and selected output backend.
///
/// Call exactly once before any output helper. Panics if called twice or if
/// the logger fails to install.
pub fn init<F: Format>(verbosity: u8, format: F) {
    self::signal::init();
    self::output::init(verbosity, format);
}

/// Runs a typed command through the shared CLI lifecycle.
///
/// Completion and parsing happen before the asynchronous runtime is entered.
/// Output initialisation precedes command execution, and every structured
/// error is rendered before its process status is returned.
pub fn entrypoint<C, O, Options, Run, Fut, Success>(options: Options, run: Run) -> ExitCode
where
    C: Parser,
    O: output::Format,
    Options: FnOnce(&C) -> (u8, O),
    Run: FnOnce(C) -> Fut,
    Fut: Future<Output = Result<Success, Error>>,
    Success: Termination,
{
    entrypoint_with(|| complete(C::command), parse::<C>, options, crate::init, run)
}

// The lifecycle steps take the command factory as a function pointer and the
// command future boxed, so completion, parsing and the runtime compile once
// here, not per binary.

fn complete(command: fn() -> Command) {
    CompleteEnv::with_factory(command).complete();
}

/// Parses the process arguments like `Parser::parse`.
fn parse<C: Parser>() -> C {
    let mut matches = matches(C::command);

    C::from_arg_matches_mut(&mut matches).unwrap_or_else(|err| exit(err, C::command))
}

fn matches(command: fn() -> Command) -> ArgMatches {
    command().get_matches()
}

fn exit(err: clap::Error, command: fn() -> Command) -> ! {
    err.format(&mut command()).exit()
}

fn entrypoint_with<C, O, Complete, Parse, Options, Initialise, Run, Fut, Success>(
    complete: Complete,
    parse: Parse,
    options: Options,
    initialise: Initialise,
    run: Run,
) -> ExitCode
where
    Complete: FnOnce(),
    Parse: FnOnce() -> C,
    Options: FnOnce(&C) -> (u8, O),
    Initialise: FnOnce(u8, O),
    Run: FnOnce(C) -> Fut,
    Fut: Future<Output = Result<Success, Error>>,
    Success: Termination,
{
    complete();

    let cmd = parse();
    let (verbosity, format) = options(&cmd);
    initialise(verbosity, format);

    execute(Box::pin(async move { run(cmd).await.map(Termination::report) }))
}

/// Drives the command on a current-thread runtime and turns its outcome into
/// the process status.
fn execute(command: Pin<Box<dyn Future<Output = Result<ExitCode, Error>> + '_>>) -> ExitCode {
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("failed to build the CLI runtime");

    match runtime.block_on(command) {
        Ok(code) => code,
        Err(err) => {
            output::failure(&err);

            let code = u8::try_from(err.exit_code()).expect("CLI exit codes must fit into a byte");
            ExitCode::from(code)
        }
    }
}

pub fn version() -> &'static str {
    match option_env!("YANET_VERSION") {
        Some("") | None => concat!(env!("CARGO_PKG_VERSION"), "-unknown"),
        Some(version) => version,
    }
}

#[cfg(test)]
mod test {
    use core::cell::RefCell;
    use std::rc::Rc;

    use super::*;

    /// Verifies that setup finishes outside Tokio and execution starts inside
    /// it.
    #[test]
    fn test_entrypoint_orders_setup_around_runtime() {
        let steps = Rc::new(RefCell::new(Vec::new()));

        let complete_steps = Rc::clone(&steps);
        let parse_steps = Rc::clone(&steps);
        let options_steps = Rc::clone(&steps);
        let initialise_steps = Rc::clone(&steps);
        let run_steps = Rc::clone(&steps);

        let _ = entrypoint_with(
            move || {
                assert!(tokio::runtime::Handle::try_current().is_err());
                complete_steps.borrow_mut().push("complete");
            },
            move || {
                assert!(tokio::runtime::Handle::try_current().is_err());
                parse_steps.borrow_mut().push("parse");
            },
            move |_: &()| {
                options_steps.borrow_mut().push("options");
                (0, ())
            },
            move |_, _| initialise_steps.borrow_mut().push("initialise"),
            move |_| async move {
                assert!(tokio::runtime::Handle::try_current().is_ok());
                run_steps.borrow_mut().push("run");
                Ok::<(), Error>(())
            },
        );

        assert_eq!(
            ["complete", "parse", "options", "initialise", "run"],
            steps.borrow().as_slice()
        );
    }
}
