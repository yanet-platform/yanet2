use crate::output::Format;

pub mod auth;
pub mod client;
pub mod completion;
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

/// Initialise the logger and output backend from a `Format` choice.
///
/// Must be called exactly once from `main` before any `output::*` helper.
/// Panics if called twice or if the logger fails to install.
pub fn init<F: Format>(verbosity: u8, format: F) {
    self::signal::init();
    self::output::init(verbosity, format);
}

pub fn version() -> &'static str {
    match option_env!("YANET_VERSION") {
        Some("") | None => concat!(env!("CARGO_PKG_VERSION"), "-unknown"),
        Some(version) => version,
    }
}
