use core::future::Future;
use std::process::{ExitCode, Termination};

use clap::Parser;
use clap_complete::CompleteEnv;

use crate::{errors::Error, output::Format};

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
pub mod timeout;

mod signal;

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
    entrypoint_with(
        || CompleteEnv::with_factory(C::command).complete(),
        C::parse,
        options,
        crate::init,
        run,
    )
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

    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("failed to build the CLI runtime");

    match runtime.block_on(async move { run(cmd).await }) {
        Ok(success) => success.report(),
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
